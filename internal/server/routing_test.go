package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/mockllm"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
)

type upstream struct {
	mock *mockllm.Server
	srv  *httptest.Server
}

// newUpstream starts a mock. headers, if non-nil, are injected into every
// request so a test can force failures (mockllm honors X-Mock-* headers).
func newUpstream(t *testing.T, opts mockllm.Options, headers map[string]string) *upstream {
	t.Helper()
	m := mockllm.New(opts)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		m.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &upstream{mock: m, srv: srv}
}

// newRoutedGateway serves alias "fast" with priority targets a/m then b/m.
func newRoutedGateway(t *testing.T, a, b *upstream, tweak func(*config.Config)) string {
	t.Helper()
	pc := func(name, url string) config.Provider {
		return config.Provider{Name: name, Type: config.ProviderOpenAI, BaseURL: url + "/v1"}
	}
	cfg := &config.Config{
		Providers: []config.Provider{pc("a", a.srv.URL), pc("b", b.srv.URL)},
		Models: []config.Model{{Alias: "fast", Strategy: config.StrategyPriority,
			Targets: []config.Target{{Provider: "a", Model: "m"}, {Provider: "b", Model: "m"}}}},
		Routing: config.Routing{
			MaxAttempts:      3,
			RetryBackoff:     config.Backoff{Base: time.Millisecond, Max: 2 * time.Millisecond},
			RetryOn:          []string{"429", "500", "502", "503", "504", "timeout", "connect_error"},
			FirstByteTimeout: 2 * time.Second,
		},
		Breaker: config.Breaker{FailureRate: 0.5, MinRequests: 3, Window: time.Minute, OpenDuration: time.Hour},
	}
	if tweak != nil {
		tweak(cfg)
	}
	cfg.ApplyDefaults()
	provs := map[string]provider.Provider{}
	for _, p := range cfg.Providers {
		provs[p.Name] = openai.New(p, "")
	}
	gw := httptest.NewServer(New(Deps{Config: cfg, Providers: provs}).Handler())
	t.Cleanup(gw.Close)
	return gw.URL
}

const chatBody = `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`
const streamBody = `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`

func TestFallbackToSecondTarget(t *testing.T) {
	a := newUpstream(t, mockllm.Options{Tokens: 2}, map[string]string{"X-Mock-Status": "503"})
	b := newUpstream(t, mockllm.Options{Tokens: 2}, nil)
	gw := newRoutedGateway(t, a, b, nil)

	for _, body := range []string{chatBody, streamBody} {
		resp := do(t, context.Background(), gw, "", body)
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !strings.Contains(string(raw), "token-2") {
			t.Fatalf("status %d body %s", resp.StatusCode, raw)
		}
		if got := resp.Header.Get(HeaderTarget); got != "b/m" {
			t.Errorf("target = %q, want b/m", got)
		}
		if got := resp.Header.Get(HeaderAttempts); got != "2" {
			t.Errorf("attempts = %q, want 2", got)
		}
	}
}

func TestAllTargetsFail(t *testing.T) {
	a := newUpstream(t, mockllm.Options{}, map[string]string{"X-Mock-Status": "500"})
	b := newUpstream(t, mockllm.Options{}, map[string]string{"X-Mock-Status": "502"})
	gw := newRoutedGateway(t, a, b, func(c *config.Config) { c.Breaker.MinRequests = 100 })

	resp := do(t, context.Background(), gw, "", chatBody)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderAttempts); got != "3" {
		t.Errorf("attempts = %q, want 3 (a, b, a)", got)
	}
	if a.mock.Requests.Load() != 2 || b.mock.Requests.Load() != 1 {
		t.Errorf("requests a=%d b=%d, want 2 and 1", a.mock.Requests.Load(), b.mock.Requests.Load())
	}
}

func TestClientErrorNotRetried(t *testing.T) {
	a := newUpstream(t, mockllm.Options{}, map[string]string{"X-Mock-Status": "400"})
	b := newUpstream(t, mockllm.Options{}, nil)
	gw := newRoutedGateway(t, a, b, nil)
	resp := do(t, context.Background(), gw, "", chatBody)
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get(HeaderAttempts) != "1" || b.mock.Requests.Load() != 0 {
		t.Fatalf("status %d attempts %s b=%d", resp.StatusCode, resp.Header.Get(HeaderAttempts), b.mock.Requests.Load())
	}
}

func TestFirstByteTimeoutFallsBack(t *testing.T) {
	a := newUpstream(t, mockllm.Options{TTFT: 2 * time.Second, Tokens: 2}, nil)
	b := newUpstream(t, mockllm.Options{Tokens: 2}, nil)
	gw := newRoutedGateway(t, a, b, func(c *config.Config) { c.Routing.FirstByteTimeout = 100 * time.Millisecond })

	start := time.Now()
	resp := do(t, context.Background(), gw, "", streamBody)
	text, _, done := streamText(t, resp.Body)
	if text != mockllm.Text(2) || !done || resp.Header.Get(HeaderTarget) != "b/m" {
		t.Fatalf("text %q done %v target %s", text, done, resp.Header.Get(HeaderTarget))
	}
	if el := time.Since(start); el > time.Second {
		t.Errorf("took %s; slow target should have been abandoned after ~100ms", el)
	}
	deadline := time.Now().Add(time.Second)
	for a.mock.ActiveStreams.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.mock.ActiveStreams.Load() != 0 {
		t.Error("abandoned upstream stream was not closed")
	}
}

func TestBreakerSkipsDeadTarget(t *testing.T) {
	a := newUpstream(t, mockllm.Options{Tokens: 1}, map[string]string{"X-Mock-Status": "503"})
	b := newUpstream(t, mockllm.Options{Tokens: 1}, nil)
	gw := newRoutedGateway(t, a, b, nil) // trips after 3 requests at ≥50% failures, open for 1h

	for range 10 {
		if resp := do(t, context.Background(), gw, "", chatBody); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	if n := a.mock.Requests.Load(); n != 3 {
		t.Errorf("dead target received %d requests, want 3 (then breaker open)", n)
	}
	resp, err := http.Get(gw + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mb, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(mb), `switchyard_breaker_state{target="a/m"} 2`) {
		t.Error("breaker gauge for a/m should be 2 (open)")
	}
}

func TestAllBreakersOpen(t *testing.T) {
	a := newUpstream(t, mockllm.Options{}, map[string]string{"X-Mock-Status": "503"})
	b := newUpstream(t, mockllm.Options{}, map[string]string{"X-Mock-Status": "503"})
	gw := newRoutedGateway(t, a, b, func(c *config.Config) { c.Breaker.MinRequests = 1 })
	do(t, context.Background(), gw, "", chatBody) // trips both
	resp := do(t, context.Background(), gw, "", chatBody)
	var e api.ErrorBody
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != http.StatusServiceUnavailable || e.Error.Code != "no_healthy_target" {
		t.Fatalf("status %d code %q", resp.StatusCode, e.Error.Code)
	}
}

func TestNoFallbackAfterFirstByte(t *testing.T) {
	a := newUpstream(t, mockllm.Options{Tokens: 10}, map[string]string{"X-Mock-Fail-After-Tokens": "3"})
	b := newUpstream(t, mockllm.Options{Tokens: 10}, nil)
	gw := newRoutedGateway(t, a, b, nil)
	resp := do(t, context.Background(), gw, "", streamBody)
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"error"`) || b.mock.Requests.Load() != 0 {
		t.Fatalf("mid-stream failure must be reported in-band without fallback; b=%d body=%s", b.mock.Requests.Load(), raw)
	}
}
