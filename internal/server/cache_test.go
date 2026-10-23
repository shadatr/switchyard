package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/cache"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/mockllm"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
)

func cachedGateway(t *testing.T) (string, *mockllm.Server, *miniredis.Miniredis) {
	t.Helper()
	m := mockllm.New(mockllm.Options{Tokens: 30})
	up := httptest.NewServer(m.Handler())
	t.Cleanup(up.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { rdb.Close() })
	cfg := &config.Config{
		Providers: []config.Provider{{Name: "mock", Type: config.ProviderOpenAI, BaseURL: up.URL + "/v1"}},
		Models:    []config.Model{{Alias: "fast", Targets: []config.Target{{Provider: "mock", Model: "mock-a"}}}},
		Cache:     config.Cache{Enabled: true, OnlyWhenTemperatureZero: true},
	}
	cfg.ApplyDefaults()
	s := New(Deps{Config: cfg, Providers: map[string]provider.Provider{"mock": openai.New(cfg.Providers[0], "")},
		Cache: cache.New(rdb, cfg.Cache, 100*time.Millisecond, nil)})
	gw := httptest.NewServer(s.Handler())
	t.Cleanup(gw.Close)
	return gw.URL, m, mr
}

func waitStored(t *testing.T, mr *miniredis.Miniredis, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(mr.Keys()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("expected %d cache entries, have %v", n, mr.Keys())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCacheHitSkipsUpstream(t *testing.T) {
	gw, m, mr := cachedGateway(t)
	body := `{"model":"fast","temperature":0,"messages":[{"role":"user","content":"cache me"}]}`
	first := do(t, context.Background(), gw, "", body)
	b1, _ := io.ReadAll(first.Body)
	if first.Header.Get(HeaderCache) != "miss" {
		t.Fatalf("first: cache %q", first.Header.Get(HeaderCache))
	}
	waitStored(t, mr, 1)

	second := do(t, context.Background(), gw, "", body)
	b2, _ := io.ReadAll(second.Body)
	if second.Header.Get(HeaderCache) != "hit" || m.Requests.Load() != 1 {
		t.Fatalf("second: cache %q, upstream requests %d", second.Header.Get(HeaderCache), m.Requests.Load())
	}
	if string(b1) != string(b2) {
		t.Errorf("cached body differs:\n%s\n%s", b1, b2)
	}

	// The same prompt as a stream is served from the same entry.
	stream := do(t, context.Background(), gw, "", strings.Replace(body, `"temperature":0`, `"temperature":0,"stream":true`, 1))
	text, _, done := streamText(t, stream.Body)
	if stream.Header.Get(HeaderCache) != "hit" || text != mockllm.Text(30) || !done || m.Requests.Load() != 1 {
		t.Fatalf("stream hit: cache %q text %q done %v upstream %d", stream.Header.Get(HeaderCache), text, done, m.Requests.Load())
	}
}

func TestStreamResponseIsCached(t *testing.T) {
	gw, m, mr := cachedGateway(t)
	body := `{"model":"fast","temperature":0,"stream":true,"messages":[{"role":"user","content":"stream me"}]}`
	resp := do(t, context.Background(), gw, "", body)
	_, _, _ = streamText(t, resp.Body)
	waitStored(t, mr, 1)
	resp = do(t, context.Background(), gw, "", strings.Replace(body, `"stream":true,`, "", 1))
	b, _ := io.ReadAll(resp.Body)
	if resp.Header.Get(HeaderCache) != "hit" || m.Requests.Load() != 1 || !strings.Contains(string(b), "token-30") {
		t.Fatalf("cache %q upstream %d body %s", resp.Header.Get(HeaderCache), m.Requests.Load(), b)
	}
}

func TestNonDeterministicBypasses(t *testing.T) {
	gw, m, _ := cachedGateway(t)
	body := `{"model":"fast","temperature":0.7,"messages":[{"role":"user","content":"x"}]}`
	for range 2 {
		if resp := do(t, context.Background(), gw, "", body); resp.Header.Get(HeaderCache) != "bypass" {
			t.Fatalf("cache %q", resp.Header.Get(HeaderCache))
		}
	}
	if m.Requests.Load() != 2 {
		t.Fatalf("upstream requests %d", m.Requests.Load())
	}
}

func TestCacheRedisDownBypasses(t *testing.T) {
	gw, _, mr := cachedGateway(t)
	mr.Close()
	resp := do(t, context.Background(), gw, "", `{"model":"fast","temperature":0,"messages":[{"role":"user","content":"x"}]}`)
	if resp.StatusCode != 200 || resp.Header.Get(HeaderCache) != "bypass" {
		t.Fatalf("status %d cache %q", resp.StatusCode, resp.Header.Get(HeaderCache))
	}
}
