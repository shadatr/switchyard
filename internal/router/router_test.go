package router

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
)

func testConfig() *config.Config {
	return &config.Config{
		Routing: config.Routing{
			MaxAttempts:  3,
			RetryBackoff: config.Backoff{Base: 100 * time.Millisecond, Max: time.Second, Jitter: true},
			RetryOn:      []string{"429", "500", "502", "503", "504", "timeout", "connect_error"},
		},
		Breaker: config.Breaker{FailureRate: 0.5, MinRequests: 20, Window: 10 * time.Second, OpenDuration: 30 * time.Second, HalfOpenMaxRequests: 1},
		Models: []config.Model{
			{Alias: "p", Strategy: config.StrategyPriority, Targets: []config.Target{{Provider: "a", Model: "1"}, {Provider: "b", Model: "2"}}},
			{Alias: "w", Strategy: config.StrategyWeighted, Targets: []config.Target{
				{Provider: "a", Model: "1", Weight: 70}, {Provider: "b", Model: "2", Weight: 20}, {Provider: "c", Model: "3", Weight: 10}}},
		},
	}
}

func TestPriorityPlan(t *testing.T) {
	r := New(testConfig(), nil)
	m, _ := r.Model("p")
	plan := r.Plan(m)
	if len(plan) != 2 || plan[0].Provider != "a" || plan[1].Provider != "b" {
		t.Fatalf("plan = %v", plan)
	}
}

func TestWeightedDistribution(t *testing.T) {
	r := New(testConfig(), nil)
	m, _ := r.Model("w")
	const n = 10_000
	first := map[string]int{}
	for range n {
		plan := r.Plan(m)
		if len(plan) != 3 {
			t.Fatalf("plan must include every target once: %v", plan)
		}
		seen := map[string]bool{}
		for _, p := range plan {
			if seen[p.Provider] {
				t.Fatalf("duplicate target in plan %v", plan)
			}
			seen[p.Provider] = true
		}
		first[plan[0].Provider]++
	}
	for prov, want := range map[string]float64{"a": 0.70, "b": 0.20, "c": 0.10} {
		got := float64(first[prov]) / n
		if math.Abs(got-want) > 0.02 {
			t.Errorf("%s chosen first %.3f of the time, want %.2f ±0.02", prov, got, want)
		}
	}
}

func TestRetryable(t *testing.T) {
	r := New(testConfig(), nil)
	cases := []struct {
		err  error
		want bool
	}{
		{provider.StatusError("x", 503, ""), true},
		{provider.StatusError("x", 429, ""), true},
		{provider.StatusError("x", 400, ""), false},
		{provider.StatusError("x", 401, ""), false},
		{&provider.Error{Code: provider.CodeTimeout, Retryable: true}, true},
		{&provider.Error{Code: provider.CodeConnect, Retryable: true}, true},
		{&provider.Error{Code: provider.CodeCanceled}, false},
		{errors.New("plain"), false},
	}
	for _, c := range cases {
		if got := r.Retryable(c.err); got != c.want {
			t.Errorf("Retryable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestCountsAsFailure(t *testing.T) {
	if CountsAsFailure(provider.StatusError("x", 400, "")) || CountsAsFailure(&provider.Error{Code: provider.CodeCanceled}) {
		t.Fatal("client errors and cancellations must not trip the breaker")
	}
	if !CountsAsFailure(provider.StatusError("x", 503, "")) || !CountsAsFailure(provider.StatusError("x", 429, "")) {
		t.Fatal("5xx and 429 must count")
	}
}

func TestBackoffBounds(t *testing.T) {
	r := New(testConfig(), nil)
	for n := 2; n <= 8; n++ {
		for range 200 {
			d := r.Backoff(n)
			base := 100 * time.Millisecond << (n - 2)
			if base > time.Second {
				base = time.Second
			}
			if d < base/2 || d > base {
				t.Fatalf("Backoff(%d) = %s, want within [%s, %s]", n, d, base/2, base)
			}
		}
	}
}

func TestOneBreakerPerTarget(t *testing.T) {
	r := New(testConfig(), nil)
	if len(r.BreakerStates()) != 3 {
		t.Fatalf("breakers = %v (shared targets must share a breaker)", r.BreakerStates())
	}
}
