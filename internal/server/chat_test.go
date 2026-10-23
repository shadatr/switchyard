package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/mockllm"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
	"github.com/shadatr/switchyard/internal/sse"
	"github.com/shadatr/switchyard/internal/store"
)

type harness struct {
	mock    *mockllm.Server
	mockURL string
	gateway string
}

type fakeAuth map[string]*store.Key

func (f fakeAuth) Authenticate(_ context.Context, k string) (*store.Key, error) {
	if k == "sy-unavailable" {
		return nil, auth.ErrUnavailable
	}
	key, ok := f[k]
	switch {
	case !ok:
		return nil, auth.ErrInvalidKey
	case key.Disabled:
		return nil, auth.ErrDisabled
	}
	return key, nil
}

// testKeys: "sy-secret" may use everything, "sy-limited" only "other",
// "sy-off" is disabled.
var testKeys = fakeAuth{
	"sy-secret":  {ID: "k1", ProjectID: "p1"},
	"sy-limited": {ID: "k2", ProjectID: "p1", AllowedModels: []string{"other"}},
	"sy-off":     {ID: "k3", ProjectID: "p1", Disabled: true},
}

func newHarness(t *testing.T, opts mockllm.Options, withAuth bool) *harness {
	t.Helper()
	m := mockllm.New(opts)
	up := httptest.NewServer(m.Handler())
	t.Cleanup(up.Close)

	pcfg := config.Provider{Name: "mock", Type: config.ProviderOpenAI, BaseURL: up.URL + "/v1",
		Timeout: 10 * time.Second, ConnectTimeout: time.Second, MaxIdleConnsPerHost: 16}
	cfg := &config.Config{
		Server:    config.Server{MaxBodyBytes: 1 << 20},
		Providers: []config.Provider{pcfg},
		Models:    []config.Model{{Alias: "fast", Strategy: config.StrategyPriority, Targets: []config.Target{{Provider: "mock", Model: "mock-a"}}}},
		Routing:   config.Routing{MaxAttempts: 1},
	}
	cfg.ApplyDefaults()
	d := Deps{Config: cfg, Providers: map[string]provider.Provider{"mock": openai.New(pcfg, "")}}
	if withAuth {
		d.Auth = testKeys
	}
	s := New(d)
	gw := httptest.NewServer(s.Handler())
	t.Cleanup(gw.Close)
	return &harness{mock: m, mockURL: up.URL, gateway: gw.URL}
}

func do(t *testing.T, ctx context.Context, url, key, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func streamText(t *testing.T, r io.Reader) (text string, usage *api.Usage, done bool) {
	t.Helper()
	var sb strings.Builder
	rd := sse.NewReader(r)
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return sb.String(), usage, done
		}
		if err != nil {
			t.Fatal(err)
		}
		if ev.IsDone() {
			done = true
			continue
		}
		var c api.ChatChunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			t.Fatalf("bad chunk %q: %v", ev.Data, err)
		}
		sb.WriteString(c.ContentText())
		if c.Usage != nil {
			usage = c.Usage
		}
	}
}

func TestNonStreamMatchesDirect(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 8}, false)
	body := `{"model":"fast","messages":[{"role":"user","content":"hello"}],"temperature":0}`
	resp := do(t, context.Background(), h.gateway, "", body)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	for _, hdr := range []string{HeaderRequestID, HeaderTarget, HeaderAttempts, HeaderCache} {
		if resp.Header.Get(hdr) == "" {
			t.Errorf("missing header %s", hdr)
		}
	}
	if got := resp.Header.Get(HeaderTarget); got != "mock/mock-a" {
		t.Errorf("target header %q", got)
	}
	var out api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if got := out.Choices[0].Message.Text(); got != mockllm.Text(8) {
		t.Errorf("content %q", got)
	}
	if out.Model != "mock-a" {
		t.Errorf("model %q (upstream model should be passed through)", out.Model)
	}
}

func TestStreamMatchesDirectAndHidesUnrequestedUsage(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 12}, false)

	resp := do(t, context.Background(), h.gateway, "", `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	text, usage, done := streamText(t, resp.Body)
	if text != mockllm.Text(12) || !done {
		t.Errorf("text=%q done=%v", text, done)
	}
	if usage != nil {
		t.Errorf("client did not request usage but got %+v", usage)
	}

	resp = do(t, context.Background(), h.gateway, "", `{"model":"fast","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	_, usage, _ = streamText(t, resp.Body)
	if usage == nil || usage.CompletionTokens != 12 {
		t.Errorf("usage = %+v, want 12 completion tokens", usage)
	}
}

func TestClientCancelClosesUpstream(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 1000, ITL: 10 * time.Millisecond}, false)
	ctx, cancel := context.WithCancel(context.Background())
	resp := do(t, ctx, h.gateway, "", `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	rd := sse.NewReader(resp.Body)
	if _, err := rd.Next(); err != nil { // wait for the stream to start
		t.Fatal(err)
	}
	if h.mock.ActiveStreams.Load() != 1 {
		t.Fatalf("expected 1 active upstream stream, got %d", h.mock.ActiveStreams.Load())
	}
	cancel()

	deadline := time.Now().Add(100 * time.Millisecond)
	for h.mock.ActiveStreams.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("upstream stream still active 100ms after client cancel")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestUpstreamErrorsMapped(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantStatus int
		wantCode   string
	}{
		{"500 becomes 502", 500, http.StatusBadGateway, "upstream_error"},
		{"503 becomes 502", 503, http.StatusBadGateway, "upstream_error"},
		{"401 (bad provider key) becomes 502", 401, http.StatusBadGateway, "upstream_error"},
		{"429 stays 429", 429, http.StatusTooManyRequests, "upstream_rate_limited"},
		{"400 passes through", 400, http.StatusBadRequest, "upstream_invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, mockllm.Options{ErrorRate: 1, ErrorStatus: tt.status}, false)
			for _, stream := range []string{"false", "true"} {
				resp := do(t, context.Background(), h.gateway, "", `{"model":"fast","stream":`+stream+`,"messages":[{"role":"user","content":"hi"}]}`)
				if resp.StatusCode != tt.wantStatus {
					t.Fatalf("stream=%s status %d, want %d", stream, resp.StatusCode, tt.wantStatus)
				}
				var e api.ErrorBody
				if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
					t.Fatal(err)
				}
				if e.Error.Code != tt.wantCode || e.Error.Message == "" {
					t.Errorf("stream=%s error = %+v", stream, e.Error)
				}
			}
		})
	}
}

func TestMidStreamFailureReportedInBand(t *testing.T) {
	// The gateway does not forward test headers, so wrap the mock in an
	// upstream that always drops the connection after 3 tokens.
	m := mockllm.New(mockllm.Options{Tokens: 10})
	dropping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Mock-Fail-After-Tokens", "3")
		m.Handler().ServeHTTP(w, r)
	}))
	defer dropping.Close()
	pcfg := config.Provider{Name: "mock", Type: config.ProviderOpenAI, BaseURL: dropping.URL + "/v1", Timeout: 5 * time.Second, ConnectTimeout: time.Second, MaxIdleConnsPerHost: 4}
	cfg := &config.Config{
		Server: config.Server{MaxBodyBytes: 1 << 20},
		Models: []config.Model{{Alias: "fast", Targets: []config.Target{{Provider: "mock", Model: "mock-a"}}}},
	}
	cfg.ApplyDefaults()
	gw := httptest.NewServer(New(Deps{Config: cfg, Providers: map[string]provider.Provider{"mock": openai.New(pcfg, "")}}).Handler())
	defer gw.Close()

	resp := do(t, context.Background(), gw.URL, "", `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	raw, _ := io.ReadAll(resp.Body)
	s := string(raw)
	if !strings.Contains(s, `"error"`) || !strings.HasSuffix(strings.TrimSpace(s), "data: [DONE]") {
		t.Fatalf("expected in-band error then [DONE], got:\n%s", s)
	}
	if !strings.Contains(s, "token-3") || strings.Contains(s, "token-4") {
		t.Errorf("expected exactly 3 tokens before failure:\n%s", s)
	}
}

func TestValidationAndAuth(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 1}, true)
	cases := []struct {
		key, body  string
		wantStatus int
	}{
		{"", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 401},
		{"wrong", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 401},
		{"sy-secret", `{not json`, 400},
		{"sy-secret", `{"model":"fast","messages":[]}`, 400},
		{"sy-secret", `{"model":"nope","messages":[{"role":"user","content":"x"}]}`, 404},
		{"sy-secret", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 200},
		{"sy-limited", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 403},
		{"sy-off", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 401},
		{"sy-unavailable", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 503},
	}
	for _, c := range cases {
		resp := do(t, context.Background(), h.gateway, c.key, c.body)
		if resp.StatusCode != c.wantStatus {
			t.Errorf("key=%q body=%s: status %d, want %d", c.key, c.body, resp.StatusCode, c.wantStatus)
		}
	}
	big := `{"model":"fast","messages":[{"role":"user","content":"` + strings.Repeat("x", 2<<20) + `"}]}`
	if resp := do(t, context.Background(), h.gateway, "sy-secret", big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d", resp.StatusCode)
	}
}

func TestModelsFilteredByKey(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 1}, true)
	req, _ := http.NewRequest(http.MethodGet, h.gateway+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sy-limited")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), `"fast"`) {
		t.Errorf("limited key must not see model fast: %s", b)
	}
}

func TestModelsAndMetrics(t *testing.T) {
	h := newHarness(t, mockllm.Options{Tokens: 1}, false)
	resp, err := http.Get(h.gateway + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"id":"fast"`) {
		t.Errorf("models: %s", b)
	}
	do(t, context.Background(), h.gateway, "", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`)
	mresp, err := http.Get(h.gateway + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	mb, _ := io.ReadAll(mresp.Body)
	for _, want := range []string{`switchyard_requests_total{alias="fast",status="200",target="mock/mock-a"} 1`, "switchyard_gateway_overhead_seconds_count 1"} {
		if !strings.Contains(string(mb), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
