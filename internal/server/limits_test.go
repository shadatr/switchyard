package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/mockllm"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
	"github.com/shadatr/switchyard/internal/ratelimit"
)

func limitedGateway(t *testing.T) (string, *miniredis.Miniredis) {
	t.Helper()
	up := httptest.NewServer(mockllm.New(mockllm.Options{Tokens: 5}).Handler())
	t.Cleanup(up.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { rdb.Close() })

	pcfg := config.Provider{Name: "mock", Type: config.ProviderOpenAI, BaseURL: up.URL + "/v1"}
	cfg := &config.Config{
		Providers: []config.Provider{pcfg},
		Models:    []config.Model{{Alias: "fast", Targets: []config.Target{{Provider: "mock", Model: "mock-a"}}}},
		Pricing:   map[string]config.Price{"mock/mock-a": {Input: 1_000_000, Output: 1_000_000}}, // $1 per token
	}
	two, budget := 2, 0.0
	keys := fakeAuth{
		"sy-rpm2":    {ID: "rpm2", RPMLimit: &two},
		"sy-broke":   {ID: "broke", MonthlyBudgetUSD: &budget},
		"sy-nolimit": {ID: "nolimit"},
	}
	cfg.ApplyDefaults()
	s := New(Deps{Config: cfg, Providers: map[string]provider.Provider{"mock": openai.New(pcfg, "")},
		Auth: keys, Limiter: ratelimit.New(rdb, 100*time.Millisecond, nil, nil)})
	gw := httptest.NewServer(s.Handler())
	t.Cleanup(gw.Close)
	return gw.URL, mr
}

func TestRateLimitedRequests(t *testing.T) {
	gw, _ := limitedGateway(t)
	for i := 1; i <= 2; i++ {
		resp := do(t, context.Background(), gw, "sy-rpm2", chatBody)
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
		if resp.Header.Get("x-ratelimit-limit-requests") != "2" {
			t.Errorf("missing rate limit headers")
		}
	}
	resp := do(t, context.Background(), gw, "sy-rpm2", chatBody)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("3rd request: status %d retry-after %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func TestBudgetExceeded(t *testing.T) {
	gw, _ := limitedGateway(t)
	if resp := do(t, context.Background(), gw, "sy-broke", chatBody); resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status %d, want 402", resp.StatusCode)
	}
}

func TestUnlimitedKeySkipsRedis(t *testing.T) {
	gw, mr := limitedGateway(t)
	resp := do(t, context.Background(), gw, "sy-nolimit", chatBody)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// A key without limits never touches Redis.
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("unexpected redis keys %v", keys)
	}
}

func TestRedisDownFailsOpen(t *testing.T) {
	gw, mr := limitedGateway(t)
	mr.Close()
	for range 5 {
		if resp := do(t, context.Background(), gw, "sy-rpm2", chatBody); resp.StatusCode != 200 {
			t.Fatalf("status %d with Redis down, want 200 (fail open)", resp.StatusCode)
		}
	}
}
