package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/sse"
	"github.com/shadatr/switchyard/internal/tokens"
)

// record captures everything known about one chat request; it feeds metrics
// now and the analytics pipeline in later phases.
type record struct {
	RequestID        string
	KeyID            string
	ProjectID        string
	Start            time.Time
	Alias            string
	Target           string
	Attempts         int
	Status           int
	ErrorCode        string
	Stream           bool
	Cache            string
	PromptTokens     int
	CompletionTokens int
	TokensEstimated  bool
	Latency          time.Duration
	TTFT             time.Duration
	Upstream         time.Duration
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	rec := &record{RequestID: ulid.Make().String(), Start: s.startTime(r.Context()), Cache: "bypass", Status: http.StatusOK}
	w.Header().Set(HeaderRequestID, rec.RequestID)
	key := KeyFrom(r.Context())
	rec.KeyID, rec.ProjectID = key.ID, key.ProjectID
	defer s.finish(rec)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.Server.MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.reject(w, rec, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "Request body too large.")
			return
		}
		s.reject(w, rec, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Could not read request body.")
		return
	}
	var req api.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.reject(w, rec, http.StatusBadRequest, "invalid_request_error", "invalid_json", "Invalid JSON: "+err.Error())
		return
	}
	rec.Alias, rec.Stream = req.Model, req.Stream
	if req.Model == "" || len(req.Messages) == 0 {
		s.reject(w, rec, http.StatusBadRequest, "invalid_request_error", "invalid_request", "model and messages are required.")
		return
	}
	model, ok := s.models[req.Model]
	if !ok {
		s.reject(w, rec, http.StatusNotFound, "invalid_request_error", "model_not_found", "Unknown model "+strconv.Quote(req.Model)+".")
		return
	}
	if !key.AllowsModel(req.Model) {
		s.reject(w, rec, http.StatusForbidden, "invalid_request_error", "model_not_allowed", "This API key may not use model "+strconv.Quote(req.Model)+".")
		return
	}

	target := model.Targets[0]
	prov := s.providers[target.Provider]
	rec.Target, rec.Attempts = target.Key(), 1
	w.Header().Set(HeaderTarget, rec.Target)
	w.Header().Set(HeaderAttempts, "1")
	w.Header().Set(HeaderCache, rec.Cache)

	if req.Stream {
		s.serveStream(w, r, rec, &req, prov, target)
	} else {
		s.serveJSON(w, r, rec, &req, prov, target)
	}
}

func (s *Server) serveJSON(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, prov provider.Provider, target config.Target) {
	upStart := s.now()
	resp, err := prov.Chat(r.Context(), req, target.Model)
	rec.Upstream = s.now().Sub(upStart)
	if err != nil {
		s.upstreamError(w, rec, err)
		return
	}
	s.applyUsage(rec, req, resp.Usage, func() int {
		n := 0
		for _, c := range resp.Choices {
			n += len(c.Message.Text())
		}
		return n
	})
	out := resp.Raw
	if out == nil {
		if out, err = json.Marshal(resp); err != nil {
			s.reject(w, rec, http.StatusBadGateway, "api_error", provider.CodeBadResponse, "Could not encode upstream response.")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
	rec.TTFT = s.now().Sub(rec.Start)
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, prov provider.Provider, target config.Target) {
	ctx := r.Context()
	clientWantsUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage

	upStart := s.now()
	st, err := prov.ChatStream(ctx, req, target.Model)
	if err != nil {
		rec.Upstream = s.now().Sub(upStart)
		s.upstreamError(w, rec, err)
		return
	}
	defer st.Close()

	sse.SetHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	sw := sse.NewWriter(w)

	buffered, _ := st.(provider.BufferedStream)
	var usage *api.Usage
	completionBytes := 0
	firstContentPending := false // first content chunk written but not yet flushed
	for {
		c, err := st.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			rec.Upstream = s.now().Sub(upStart)
			if ctx.Err() != nil {
				rec.Status, rec.ErrorCode = 499, provider.CodeCanceled
				return
			}
			// Headers are already sent: report the failure in-band.
			rec.Status, rec.ErrorCode = http.StatusBadGateway, errorCode(err)
			s.metrics.UpstreamErrors.WithLabelValues(rec.Target, rec.ErrorCode).Inc()
			b, _ := json.Marshal(api.ErrorBody{Error: api.ErrorDetail{Message: "Upstream stream failed.", Type: "api_error", Code: rec.ErrorCode}})
			_ = sw.WriteData(b)
			_ = sw.WriteDone()
			s.applyUsage(rec, req, usage, func() int { return completionBytes })
			return
		}
		if c.Usage != nil {
			usage = c.Usage
			if !clientWantsUsage && len(c.Choices) == 0 {
				continue // usage-only chunk the client didn't ask for
			}
		}
		text := c.ContentText()
		completionBytes += len(text)
		data := c.Raw
		if data == nil {
			if data, err = json.Marshal(c); err != nil {
				continue
			}
		}
		if err := sw.Write(data); err != nil {
			rec.Upstream = s.now().Sub(upStart)
			rec.Status, rec.ErrorCode = 499, provider.CodeCanceled
			return
		}
		if rec.TTFT == 0 && text != "" {
			firstContentPending = true
		}
		// Flush unless the next chunk is already buffered: coalesces bursts
		// into one write without ever delaying a chunk on network I/O.
		if buffered == nil || !buffered.HasBufferedChunk() {
			if err := sw.Flush(); err != nil {
				rec.Upstream = s.now().Sub(upStart)
				rec.Status, rec.ErrorCode = 499, provider.CodeCanceled
				return
			}
			if firstContentPending {
				rec.TTFT = s.now().Sub(rec.Start)
				firstContentPending = false
			}
		}
	}
	rec.Upstream = s.now().Sub(upStart)
	_ = sw.WriteDone()
	s.applyUsage(rec, req, usage, func() int { return completionBytes })
}

// applyUsage fills token counts from upstream usage, or estimates them.
func (s *Server) applyUsage(rec *record, req *api.ChatRequest, u *api.Usage, completionBytes func() int) {
	if u != nil {
		rec.PromptTokens, rec.CompletionTokens = u.PromptTokens, u.CompletionTokens
		return
	}
	rec.TokensEstimated = true
	rec.PromptTokens = tokens.Estimate(req.PromptText())
	rec.CompletionTokens = tokens.EstimateBytes(completionBytes())
}

// upstreamError maps a provider error to an OpenAI-style client error.
func (s *Server) upstreamError(w http.ResponseWriter, rec *record, err error) {
	pe, ok := provider.AsError(err)
	if !ok {
		s.reject(w, rec, http.StatusBadGateway, "api_error", provider.CodeUpstream, "Upstream request failed.")
		return
	}
	s.metrics.UpstreamErrors.WithLabelValues(rec.Target, pe.Code).Inc()
	switch {
	case pe.Code == provider.CodeCanceled:
		rec.Status, rec.ErrorCode = 499, pe.Code
	case pe.Code == provider.CodeTimeout:
		s.reject(w, rec, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "Upstream timed out.")
	case pe.Status == http.StatusTooManyRequests:
		s.reject(w, rec, http.StatusTooManyRequests, "rate_limit_error", "upstream_rate_limited", "Upstream rate limit reached: "+pe.Message)
	case pe.Code == provider.CodeInvalid && pe.Status != http.StatusUnauthorized && pe.Status != http.StatusForbidden:
		s.reject(w, rec, pe.Status, "invalid_request_error", "upstream_invalid_request", pe.Message)
	default:
		s.reject(w, rec, http.StatusBadGateway, "api_error", "upstream_error", "Upstream request failed.")
	}
}

func (s *Server) reject(w http.ResponseWriter, rec *record, status int, typ, code, msg string) {
	rec.Status, rec.ErrorCode = status, code
	writeError(w, status, typ, code, msg)
}

func errorCode(err error) string {
	if pe, ok := provider.AsError(err); ok {
		return pe.Code
	}
	return provider.CodeUpstream
}

func (s *Server) finish(rec *record) {
	rec.Latency = s.now().Sub(rec.Start)
	if rec.TTFT == 0 {
		rec.TTFT = rec.Latency
	}
	alias, target := rec.Alias, rec.Target
	if alias == "" {
		alias = "unknown"
	}
	if target == "" {
		target = "none"
	}
	m := s.metrics
	m.Requests.WithLabelValues(alias, target, strconv.Itoa(rec.Status)).Inc()
	m.Duration.WithLabelValues(alias, strconv.FormatBool(rec.Stream)).Observe(rec.Latency.Seconds())
	if rec.Status == http.StatusOK {
		m.TTFT.WithLabelValues(alias).Observe(rec.TTFT.Seconds())
	}
	if rec.Upstream > 0 {
		m.Overhead.Observe((rec.Latency - rec.Upstream).Seconds())
		m.Attempts.Observe(float64(rec.Attempts))
	}
}
