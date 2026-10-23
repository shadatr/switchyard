package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/router"
	"github.com/shadatr/switchyard/internal/sse"
	"github.com/shadatr/switchyard/internal/tokens"
)

// record captures everything known about one chat request; it feeds metrics
// and the analytics pipeline.
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
	Upstream         time.Duration // time spent waiting on upstreams (all attempts)
	Backoff          time.Duration // time spent sleeping between attempts
}

// errNoHealthyTarget is returned when every target's breaker is open.
var errNoHealthyTarget = errors.New("no healthy target")

var errFirstByteTimeout = errors.New("first byte timeout")

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
	model, ok := s.router.Model(req.Model)
	if !ok {
		s.reject(w, rec, http.StatusNotFound, "invalid_request_error", "model_not_found", "Unknown model "+strconv.Quote(req.Model)+".")
		return
	}
	if !key.AllowsModel(req.Model) {
		s.reject(w, rec, http.StatusForbidden, "invalid_request_error", "model_not_allowed", "This API key may not use model "+strconv.Quote(req.Model)+".")
		return
	}
	w.Header().Set(HeaderCache, rec.Cache)

	if req.Stream {
		s.serveStream(w, r, rec, &req, model)
	} else {
		s.serveJSON(w, r, rec, &req, model)
	}
}

// attempt runs fn against targets in router order until one succeeds, the
// error is not retryable, or the attempt budget is spent. fn returns the
// upstream error (nil on success).
func (s *Server) attempt(ctx context.Context, rec *record, model config.Model, fn func(context.Context, config.Target, provider.Provider) error) error {
	plan := s.router.Plan(model)
	var lastErr error = errNoHealthyTarget
	attempts, skipped := 0, 0
	for i := 0; attempts < s.router.MaxAttempts() && skipped < len(plan); i++ {
		t := plan[i%len(plan)]
		b := s.router.Breaker(t)
		if !b.Allow() {
			skipped++
			continue
		}
		skipped = 0
		attempts++
		if attempts > 1 {
			d := s.router.Backoff(attempts)
			if !router.Sleep(ctx, d) {
				b.Cancel()
				return &provider.Error{Code: provider.CodeCanceled, Message: "client canceled"}
			}
			rec.Backoff += d
		}
		rec.Target, rec.Attempts = t.Key(), attempts

		up := s.now()
		err := fn(ctx, t, s.providers[t.Provider])
		rec.Upstream += s.now().Sub(up)
		if err == nil {
			b.Success()
			return nil
		}
		switch {
		case router.CountsAsFailure(err):
			b.Failure()
		case ctx.Err() != nil:
			b.Cancel()
		default:
			b.Success() // the target answered; the request itself was bad
		}
		s.metrics.UpstreamErrors.WithLabelValues(t.Key(), errorCode(err)).Inc()
		lastErr = err
		if ctx.Err() != nil || !s.router.Retryable(err) {
			return err
		}
	}
	return lastErr
}

func (s *Server) serveJSON(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, model config.Model) {
	var resp *api.ChatResponse
	err := s.attempt(r.Context(), rec, model, func(ctx context.Context, t config.Target, p provider.Provider) error {
		var err error
		resp, err = p.Chat(ctx, req, t.Model)
		return err
	})
	s.setRoutingHeaders(w, rec)
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

// openStream starts a stream and waits for its first chunk, falling back to
// other targets on failure. Nothing has been written to the client yet, so
// fallback is still safe. The returned release func must be called when the
// stream is done.
func (s *Server) openStream(ctx context.Context, rec *record, req *api.ChatRequest, model config.Model) (provider.Stream, *api.ChatChunk, func(), error) {
	var (
		st      provider.Stream
		first   *api.ChatChunk
		release func()
	)
	err := s.attempt(ctx, rec, model, func(ctx context.Context, t config.Target, p provider.Provider) error {
		actx, cancel := context.WithCancelCause(ctx)
		timer := time.AfterFunc(s.router.FirstByteTimeout(), func() { cancel(errFirstByteTimeout) })
		stream, err := p.ChatStream(actx, req, t.Model)
		var chunk *api.ChatChunk
		if err == nil {
			chunk, err = stream.Next()
			if errors.Is(err, io.EOF) {
				err = &provider.Error{Provider: t.Provider, Code: provider.CodeBadResponse, Message: "empty stream", Retryable: true}
			}
		}
		if !timer.Stop() && err == nil {
			err = errFirstByteTimeout // the timer fired just as the first chunk arrived
		}
		if err != nil {
			if stream != nil {
				_ = stream.Close()
			}
			if context.Cause(actx) == errFirstByteTimeout && ctx.Err() == nil {
				err = &provider.Error{Provider: t.Provider, Code: provider.CodeTimeout, Retryable: true,
					Message: fmt.Sprintf("no first chunk within %s", s.router.FirstByteTimeout())}
			}
			cancel(nil)
			return err
		}
		st, first, release = stream, chunk, func() { _ = stream.Close(); cancel(nil) }
		return nil
	})
	return st, first, release, err
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, model config.Model) {
	ctx := r.Context()
	clientWantsUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage

	st, first, release, err := s.openStream(ctx, rec, req, model)
	s.setRoutingHeaders(w, rec)
	if err != nil {
		s.upstreamError(w, rec, err)
		return
	}
	defer release()
	upStart := s.now() // time before the first chunk is already in rec.Upstream

	sse.SetHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	sw := sse.NewWriter(w)
	buffered, _ := st.(provider.BufferedStream)

	var usage *api.Usage
	completionBytes := 0
	firstContentPending := false // first content chunk written but not yet flushed
	clientGone := func() { rec.Status, rec.ErrorCode = 499, provider.CodeCanceled }

	c := first
	for {
		if c.Usage != nil {
			usage = c.Usage
		}
		usageOnly := c.Usage != nil && len(c.Choices) == 0
		if !usageOnly || clientWantsUsage {
			text := c.ContentText()
			completionBytes += len(text)
			data := c.Raw
			if data == nil {
				data, _ = json.Marshal(c)
			}
			if err := sw.Write(data); err != nil {
				clientGone()
				break
			}
			if rec.TTFT == 0 && text != "" {
				firstContentPending = true
			}
			// Flush unless the next chunk is already buffered: coalesces
			// bursts into one write without delaying a chunk on network I/O.
			if buffered == nil || !buffered.HasBufferedChunk() {
				if err := sw.Flush(); err != nil {
					clientGone()
					break
				}
				if firstContentPending {
					rec.TTFT = s.now().Sub(rec.Start)
					firstContentPending = false
				}
			}
		}

		c, err = st.Next()
		if errors.Is(err, io.EOF) {
			_ = sw.WriteDone()
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				clientGone()
				break
			}
			// Headers are already sent, so no fallback: report in-band.
			rec.Status, rec.ErrorCode = http.StatusBadGateway, errorCode(err)
			s.metrics.UpstreamErrors.WithLabelValues(rec.Target, rec.ErrorCode).Inc()
			if router.CountsAsFailure(err) {
				s.router.BreakerByKey(rec.Target).Failure()
			}
			b, _ := json.Marshal(api.ErrorBody{Error: api.ErrorDetail{Message: "Upstream stream failed.", Type: "api_error", Code: rec.ErrorCode}})
			_ = sw.WriteData(b)
			_ = sw.WriteDone()
			break
		}
	}
	rec.Upstream += s.now().Sub(upStart)
	s.applyUsage(rec, req, usage, func() int { return completionBytes })
}

func (s *Server) setRoutingHeaders(w http.ResponseWriter, rec *record) {
	if rec.Target != "" {
		w.Header().Set(HeaderTarget, rec.Target)
	}
	w.Header().Set(HeaderAttempts, strconv.Itoa(rec.Attempts))
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
	if errors.Is(err, errNoHealthyTarget) {
		s.reject(w, rec, http.StatusServiceUnavailable, "api_error", "no_healthy_target", "All upstream targets for this model are temporarily unavailable.")
		return
	}
	pe, ok := provider.AsError(err)
	if !ok {
		s.reject(w, rec, http.StatusBadGateway, "api_error", provider.CodeUpstream, "Upstream request failed.")
		return
	}
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
		m.Overhead.Observe((rec.Latency - rec.Upstream - rec.Backoff).Seconds())
		m.Attempts.Observe(float64(rec.Attempts))
	}
}
