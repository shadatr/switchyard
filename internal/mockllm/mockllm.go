// Package mockllm implements a fake OpenAI-compatible upstream with
// controllable latency and failures. It is used by tests (in-process via
// httptest) and by benchmarks (as the cmd/mockllm binary).
package mockllm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/sse"
)

// Options control default behavior. Per-request headers override them:
// X-Mock-TTFT, X-Mock-ITL (durations), X-Mock-Tokens, X-Mock-Status,
// X-Mock-Fail-After-Tokens (ints).
type Options struct {
	TTFT        time.Duration // delay before the first token
	ITL         time.Duration // inter-token latency
	Tokens      int           // completion tokens to generate
	ErrorRate   float64       // probability [0,1] of failing a request
	ErrorStatus int           // status used for random failures
	Jitter      float64       // ±fraction applied to TTFT and ITL
}

// DefaultOptions mirror PLAN.md §7.
func DefaultOptions() Options {
	return Options{TTFT: 200 * time.Millisecond, ITL: 15 * time.Millisecond, Tokens: 100, ErrorStatus: http.StatusServiceUnavailable}
}

// Server is the mock upstream. Counters are exported for tests.
type Server struct {
	opts Options

	Requests      atomic.Int64 // chat requests received
	ActiveStreams atomic.Int64 // streams currently being written
}

// New returns a Server with the given options.
func New(opts Options) *Server {
	if opts.ErrorStatus == 0 {
		opts.ErrorStatus = http.StatusServiceUnavailable
	}
	return &Server{opts: opts}
}

// Handler returns the HTTP handler serving /v1/models and /v1/chat/completions.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	return mux
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mock-a","object":"model","owned_by":"mockllm"},{"id":"mock-b","object":"model","owned_by":"mockllm"}]}`))
}

type plan struct {
	ttft, itl       time.Duration
	tokens          int
	status          int
	failAfterTokens int // -1 = never
}

func (s *Server) planFor(r *http.Request) plan {
	p := plan{ttft: s.opts.TTFT, itl: s.opts.ITL, tokens: s.opts.Tokens, status: http.StatusOK, failAfterTokens: -1}
	h := r.Header
	if d, err := time.ParseDuration(h.Get("X-Mock-TTFT")); err == nil {
		p.ttft = d
	}
	if d, err := time.ParseDuration(h.Get("X-Mock-ITL")); err == nil {
		p.itl = d
	}
	if n, err := strconv.Atoi(h.Get("X-Mock-Tokens")); err == nil && n >= 0 {
		p.tokens = n
	}
	if n, err := strconv.Atoi(h.Get("X-Mock-Fail-After-Tokens")); err == nil && n >= 0 {
		p.failAfterTokens = n
	}
	if n, err := strconv.Atoi(h.Get("X-Mock-Status")); err == nil && n >= 100 {
		p.status = n
	} else if s.opts.ErrorRate > 0 && rand.Float64() < s.opts.ErrorRate {
		p.status = s.opts.ErrorStatus
	}
	p.ttft = s.jitter(p.ttft)
	p.itl = s.jitter(p.itl)
	return p
}

func (s *Server) jitter(d time.Duration) time.Duration {
	if s.opts.Jitter <= 0 || d <= 0 {
		return d
	}
	f := 1 + s.opts.Jitter*(rand.Float64()*2-1)
	return time.Duration(float64(d) * f)
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	var req api.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	p := s.planFor(r)
	if p.status != http.StatusOK {
		writeError(w, p.status, "upstream_error", fmt.Sprintf("mock failure (status %d)", p.status))
		return
	}

	promptTokens := estimate(req.PromptText())
	if req.Stream {
		s.stream(r.Context(), w, &req, p, promptTokens)
		return
	}

	if !sleep(r.Context(), p.ttft+time.Duration(max(p.tokens-1, 0))*p.itl) {
		return
	}
	var text strings.Builder
	for i := 1; i <= p.tokens; i++ {
		text.WriteString(Token(i))
	}
	stop := "stop"
	resp := api.ChatResponse{
		ID:      "chatcmpl-mock-" + strconv.FormatInt(s.Requests.Load(), 10),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []api.Choice{{Index: 0, Message: api.TextMessage("assistant", text.String()), FinishReason: &stop}},
		Usage:   &api.Usage{PromptTokens: promptTokens, CompletionTokens: p.tokens, TotalTokens: promptTokens + p.tokens},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) stream(ctx context.Context, w http.ResponseWriter, req *api.ChatRequest, p plan, promptTokens int) {
	s.ActiveStreams.Add(1)
	defer s.ActiveStreams.Add(-1)

	sse.SetHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	sw := sse.NewWriter(w)
	id := "chatcmpl-mock-" + strconv.FormatInt(s.Requests.Load(), 10)
	created := time.Now().Unix()

	chunk := func(delta api.Delta, finish *string) []byte {
		b, _ := json.Marshal(api.ChatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []api.ChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
		})
		return b
	}

	if !sleep(ctx, p.ttft) {
		return
	}
	if sw.WriteData(chunk(api.Delta{Role: "assistant"}, nil)) != nil {
		return
	}
	for i := 1; i <= p.tokens; i++ {
		if i > 1 && !sleep(ctx, p.itl) {
			return
		}
		if p.failAfterTokens >= 0 && i > p.failAfterTokens {
			panic(http.ErrAbortHandler) // drop the connection mid-stream
		}
		if sw.WriteData(chunk(api.Delta{Content: Token(i)}, nil)) != nil {
			return
		}
	}
	stop := "stop"
	if sw.WriteData(chunk(api.Delta{}, &stop)) != nil {
		return
	}
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		b, _ := json.Marshal(api.ChatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []api.ChunkChoice{},
			Usage:   &api.Usage{PromptTokens: promptTokens, CompletionTokens: p.tokens, TotalTokens: promptTokens + p.tokens},
		})
		if sw.WriteData(b) != nil {
			return
		}
	}
	_ = sw.WriteDone()
}

// Token returns the deterministic text of the i-th generated token (1-based).
func Token(i int) string {
	if i == 1 {
		return "token-1"
	}
	return " token-" + strconv.Itoa(i)
}

// Text returns the full deterministic completion for n tokens.
func Text(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		sb.WriteString(Token(i))
	}
	return sb.String()
}

func estimate(s string) int { return (len(s) + 3) / 4 }

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func writeError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: api.ErrorDetail{Message: msg, Type: typ}})
}
