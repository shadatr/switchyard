package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/cache"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/live"
	"github.com/shadatr/switchyard/internal/logsink"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/ratelimit"
	"github.com/shadatr/switchyard/internal/router"
	"github.com/shadatr/switchyard/internal/sse"
	"github.com/shadatr/switchyard/internal/tokens"
	"github.com/shadatr/switchyard/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	CostUSD          float64
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
	defer s.finish(r.Context(), rec)

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
	reserved := 0
	if s.limiter != nil {
		_, span := tracing.Tracer().Start(r.Context(), "ratelimit.admit")
		d := s.limiter.Admit(r.Context(), key, tokens.Estimate(req.PromptText()))
		span.SetAttributes(attribute.Bool("allowed", d.Allowed), attribute.Bool("degraded", d.Degraded))
		span.End()
		setRateLimitHeaders(w.Header(), d)
		if !d.Allowed {
			s.rejectLimited(w, rec, d)
			return
		}
		reserved = d.TokensReserved
		defer func() {
			actual := rec.PromptTokens + rec.CompletionTokens
			if rec.Cache == "hit" {
				actual = 0 // cached answers don't consume token quota
			}
			if rec.Upstream > 0 || reserved > 0 {
				s.limiter.Settle(key, reserved, actual, rec.CostUSD)
			}
		}()
	}

	cacheKey, cacheable := s.cache.Key(key.ProjectID, &req)
	if cacheable {
		_, span := tracing.Tracer().Start(r.Context(), "cache.lookup")
		e, err := s.cache.Get(r.Context(), cacheKey)
		span.SetAttributes(attribute.Bool("hit", e != nil))
		span.End()
		switch {
		case err != nil:
			rec.Cache, cacheKey = "bypass", ""
		case e != nil:
			rec.Cache = "hit"
			s.metrics.Cache.WithLabelValues("hit").Inc()
			s.serveCached(w, rec, &req, e)
			return
		default:
			rec.Cache = "miss"
		}
		s.metrics.Cache.WithLabelValues(rec.Cache).Inc()
	} else {
		cacheKey = ""
	}
	w.Header().Set(HeaderCache, rec.Cache)

	if req.Stream {
		s.serveStream(w, r, rec, &req, model, cacheKey)
	} else {
		s.serveJSON(w, r, rec, &req, model, cacheKey)
	}
}

// serveCached answers from a cache entry without touching any upstream.
func (s *Server) serveCached(w http.ResponseWriter, rec *record, req *api.ChatRequest, e *cache.Entry) {
	h := w.Header()
	h.Set(HeaderCache, "hit")
	h.Set(HeaderTarget, e.Target)
	h.Set(HeaderAttempts, "0")
	rec.Target = e.Target
	rec.PromptTokens, rec.CompletionTokens = e.Usage.PromptTokens, e.Usage.CompletionTokens
	if !req.Stream {
		h.Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(e.Response())
		rec.TTFT = s.now().Sub(rec.Start)
		return
	}
	sse.SetHeaders(h)
	w.WriteHeader(http.StatusOK)
	sw := sse.NewWriter(w)
	for _, c := range e.Chunks(req.StreamOptions != nil && req.StreamOptions.IncludeUsage) {
		if sw.Write(c) != nil {
			rec.Status, rec.ErrorCode = 499, provider.CodeCanceled
			return
		}
	}
	_ = sw.WriteDone()
	rec.TTFT = s.now().Sub(rec.Start)
}

func (s *Server) storeInCache(key string, rec *record, e *cache.Entry) {
	if key == "" || rec.Status != http.StatusOK {
		return
	}
	e.Target = rec.Target
	if s.cache.Put(key, e) {
		s.metrics.Cache.WithLabelValues("store").Inc()
	}
}

func setRateLimitHeaders(h http.Header, d ratelimit.Decision) {
	if d.LimitRequests >= 0 {
		h.Set("x-ratelimit-limit-requests", strconv.Itoa(d.LimitRequests))
		h.Set("x-ratelimit-remaining-requests", strconv.Itoa(max(d.RemainingRequests, 0)))
	}
	if d.LimitTokens >= 0 {
		h.Set("x-ratelimit-limit-tokens", strconv.Itoa(d.LimitTokens))
		h.Set("x-ratelimit-remaining-tokens", strconv.Itoa(max(d.RemainingTokens, 0)))
	}
}

func (s *Server) rejectLimited(w http.ResponseWriter, rec *record, d ratelimit.Decision) {
	if d.Kind == ratelimit.KindBudget {
		s.reject(w, rec, http.StatusPaymentRequired, "insufficient_quota", "budget_exceeded", "This API key has used its monthly budget.")
		return
	}
	secs := int((d.RetryAfter + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	msg := "Rate limit reached for requests per minute."
	if d.Kind == ratelimit.KindTPM {
		msg = "Rate limit reached for tokens per minute."
	}
	s.reject(w, rec, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", msg)
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

		actx, span := tracing.Tracer().Start(ctx, "upstream.attempt", trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attribute.String("target", t.Key()), attribute.Int("attempt", attempts)))
		up := s.now()
		err := fn(actx, t, s.providers[t.Provider])
		rec.Upstream += s.now().Sub(up)
		if err != nil {
			span.SetStatus(codes.Error, errorCode(err))
		}
		span.End()
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

func (s *Server) serveJSON(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, model config.Model, cacheKey string) {
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
	rec.CostUSD = s.pricing.Cost(rec.Target, rec.PromptTokens, rec.CompletionTokens)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
	rec.TTFT = s.now().Sub(rec.Start)

	if cacheKey != "" && len(resp.Choices) == 1 && resp.Choices[0].Message.Extra["tool_calls"] == nil {
		e := &cache.Entry{Model: resp.Model, Content: resp.Choices[0].Message.Text(), Raw: resp.Raw,
			Usage: api.Usage{PromptTokens: rec.PromptTokens, CompletionTokens: rec.CompletionTokens, TotalTokens: rec.PromptTokens + rec.CompletionTokens}}
		if f := resp.Choices[0].FinishReason; f != nil {
			e.FinishReason = *f
		}
		s.storeInCache(cacheKey, rec, e)
	}
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

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, rec *record, req *api.ChatRequest, model config.Model, cacheKey string) {
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

	// Accumulate the answer for the cache; give up on tool calls, multiple
	// choices or oversized answers.
	var cached *cache.Entry
	var content strings.Builder
	if cacheKey != "" {
		cached = &cache.Entry{Model: first.Model}
	}
	complete := false
	clientGone := func() { rec.Status, rec.ErrorCode = 499, provider.CodeCanceled }

	c := first
	for {
		if c.Usage != nil {
			usage = c.Usage
		}
		if cached != nil {
			for _, ch := range c.Choices {
				if ch.Index != 0 || len(ch.Delta.ToolCalls) > 0 || content.Len() > s.cfg.Cache.MaxEntryBytes {
					cached = nil
					break
				}
				content.WriteString(ch.Delta.Content)
				if ch.FinishReason != nil {
					cached.FinishReason = *ch.FinishReason
				}
			}
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
			complete = true
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
	rec.CostUSD = s.pricing.Cost(rec.Target, rec.PromptTokens, rec.CompletionTokens)
	if cached != nil && complete {
		cached.Content = content.String()
		cached.Usage = api.Usage{PromptTokens: rec.PromptTokens, CompletionTokens: rec.CompletionTokens, TotalTokens: rec.PromptTokens + rec.CompletionTokens}
		s.storeInCache(cacheKey, rec, cached)
	}
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

func (s *Server) finish(ctx context.Context, rec *record) {
	rec.Latency = s.now().Sub(rec.Start)
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(attribute.String("request_id", rec.RequestID), attribute.String("model", rec.Alias),
			attribute.String("target", rec.Target), attribute.Int("status", rec.Status), attribute.String("cache", rec.Cache),
			attribute.Int("prompt_tokens", rec.PromptTokens), attribute.Int("completion_tokens", rec.CompletionTokens),
			attribute.Float64("cost_usd", rec.CostUSD), attribute.Int("attempts", rec.Attempts))
		if rec.Status >= 400 {
			span.SetStatus(codes.Error, rec.ErrorCode)
		}
	}
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
	overhead := rec.Latency - rec.Upstream - rec.Backoff
	if rec.Upstream > 0 {
		m.Overhead.Observe(overhead.Seconds())
		m.Attempts.Observe(float64(rec.Attempts))
	}
	if s.sink != nil {
		s.sink.Enqueue(logsink.Record{
			TS: rec.Start, RequestID: rec.RequestID, ProjectID: rec.ProjectID, KeyID: rec.KeyID,
			ModelAlias: rec.Alias, Target: rec.Target, Attempts: uint8(min(rec.Attempts, 255)), Status: uint16(rec.Status),
			ErrorCode: rec.ErrorCode, Stream: rec.Stream, Cache: rec.Cache,
			PromptTokens: uint32(max(rec.PromptTokens, 0)), CompletionTokens: uint32(max(rec.CompletionTokens, 0)),
			TokensEstimated: rec.TokensEstimated, CostUSD: rec.CostUSD,
			LatencyMs: durMs(rec.Latency), TTFTMs: durMs(rec.TTFT), UpstreamMs: durMs(rec.Upstream),
			GatewayOverheadUs: uint32(max(overhead.Microseconds(), 0)),
		})
	}
	if s.live != nil {
		s.live.Publish(live.Event{
			TS: rec.Start.UnixMilli(), RequestID: rec.RequestID, ProjectID: rec.ProjectID, KeyID: rec.KeyID,
			Alias: rec.Alias, Target: rec.Target, Status: rec.Status, ErrorCode: rec.ErrorCode, Stream: rec.Stream,
			Cache: rec.Cache, Attempts: rec.Attempts, PromptTokens: rec.PromptTokens, CompletionTokens: rec.CompletionTokens,
			CostUSD: rec.CostUSD, LatencyMs: float64(rec.Latency.Microseconds()) / 1000, TTFTMs: float64(rec.TTFT.Microseconds()) / 1000,
		})
	}
}

func durMs(d time.Duration) float32 { return float32(max(d.Microseconds(), 0)) / 1000 }
