package ratelimit

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/store"
)

// newLimiter uses a real Redis when SWITCHYARD_TEST_REDIS is set (e.g.
// localhost:16379 from docker compose), otherwise an in-process miniredis.
func newLimiter(t *testing.T) (*Limiter, *metrics.Metrics, func()) {
	t.Helper()
	m := metrics.New()
	if addr := os.Getenv("SWITCHYARD_TEST_REDIS"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { rdb.Close() })
		return New(rdb, 200*time.Millisecond, m, nil), m, func() { t.Skip("cannot stop a real Redis") }
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { rdb.Close() })
	return New(rdb, 200*time.Millisecond, m, nil), m, mr.Close
}

var keySeq atomic.Int64

func key(rpm, tpm int, budget float64) *store.Key {
	k := &store.Key{ID: "test-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "") + "-" + string(rune('a'+keySeq.Add(1)%26))}
	if rpm > 0 {
		k.RPMLimit = &rpm
	}
	if tpm > 0 {
		k.TPMLimit = &tpm
	}
	if budget >= 0 {
		k.MonthlyBudgetUSD = &budget
	}
	return k
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUnlimitedKeySkipsRedis(t *testing.T) {
	l, _, stop := newLimiter(t)
	stop() // Redis down must not matter for a key without limits
	if d := l.Admit(context.Background(), key(0, 0, -1), 100); !d.Allowed || d.Degraded {
		t.Fatalf("decision %+v", d)
	}
}

func TestRPMLimitAndRetryAfter(t *testing.T) {
	l, m, _ := newLimiter(t)
	k := key(600, 0, -1)
	ctx := context.Background()
	start := time.Now()
	n := 0
	var d Decision
	for {
		d = l.Admit(ctx, k, 0)
		if !d.Allowed {
			break
		}
		n++
	}
	refill := int(time.Since(start).Seconds()*10) + 1 // 10 tokens/s
	if n < 600 || n > 600+refill || d.Kind != KindRPM {
		t.Fatalf("allowed %d before rejection (want 600..%d), decision %+v", n, 600+refill, d)
	}
	// 600/min = 1 token per 100ms.
	if d.RetryAfter <= 0 || d.RetryAfter > 100*time.Millisecond {
		t.Errorf("retry after %s, want (0, 100ms]", d.RetryAfter)
	}
	if got := testutil.ToFloat64(m.RateLimited.WithLabelValues(KindRPM)); got != 1 {
		t.Errorf("rate limited metric = %v", got)
	}
	time.Sleep(110 * time.Millisecond)
	if d := l.Admit(ctx, k, 0); !d.Allowed {
		t.Fatalf("bucket should have refilled one token: %+v", d)
	}
}

func TestRPMConcurrentNeverExceedsLimit(t *testing.T) {
	l, _, _ := newLimiter(t)
	k := key(600, 0, -1)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for range 100 {
		wg.Go(func() {
			for range 100 {
				if l.Admit(context.Background(), k, 0).Allowed {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	refill := int64(time.Since(start).Seconds()*10) + 1 // 10 tokens/s
	if got := allowed.Load(); got > 600+refill || got < 600 {
		t.Fatalf("allowed %d of 10000, want 600 (+%d refill)", got, refill)
	}
}

func TestTPMReserveRefundAndSettle(t *testing.T) {
	l, _, _ := newLimiter(t)
	k := key(1000, 1000, -1)
	ctx := context.Background()
	for range 2 {
		if d := l.Admit(ctx, k, 400); !d.Allowed || d.TokensReserved != 400 {
			t.Fatalf("decision %+v", d)
		}
	}
	d := l.Admit(ctx, k, 400) // only ~200 tokens left
	if d.Allowed || d.Kind != KindTPM {
		t.Fatalf("third request: %+v", d)
	}
	// The rejected request's RPM token must be refunded: 1000 - 2 used.
	eventually(t, func() bool {
		d := l.Admit(ctx, key2(k, 0), 0) // probe RPM without TPM
		return d.RemainingRequests >= 996
	})

	// Settle the first request with 900 actual tokens: 500 more are taken.
	l.Settle(k, 400, 900, 0)
	eventually(t, func() bool {
		d := l.Admit(ctx, k, 1)
		return !d.Allowed && d.Kind == KindTPM
	})
}

// key2 returns a copy of k without TPM (to read the RPM bucket alone).
func key2(k *store.Key, _ int) *store.Key {
	c := *k
	c.TPMLimit = nil
	return &c
}

func TestBudget(t *testing.T) {
	l, _, _ := newLimiter(t)
	k := key(0, 0, 1.0)
	ctx := context.Background()
	if d := l.Admit(ctx, k, 10); !d.Allowed {
		t.Fatalf("decision %+v", d)
	}
	l.Settle(k, 0, 0, 0.75)
	l.Settle(k, 0, 0, 0.25)
	eventually(t, func() bool {
		spent, _ := l.Spent(ctx, k.ID)
		return spent >= 1.0
	})
	if d := l.Admit(ctx, k, 10); d.Allowed || d.Kind != KindBudget {
		t.Fatalf("over budget: %+v", d)
	}
}

func TestBudgetKeyFormat(t *testing.T) {
	ts := time.Date(2026, 1, 31, 23, 59, 0, 0, time.FixedZone("TR", 3*3600))
	if got := BudgetKey("k", ts); got != "budget:k:202601" {
		t.Fatalf("BudgetKey = %q (must use UTC month)", got)
	}
}

func TestRedisDownFailsOpen(t *testing.T) {
	l, m, stop := newLimiter(t)
	stop()
	d := l.Admit(context.Background(), key(1, 1, 0), 10)
	if !d.Allowed || !d.Degraded {
		t.Fatalf("decision %+v, want allowed+degraded", d)
	}
	if got := testutil.ToFloat64(m.DependencyErrors.WithLabelValues("redis")); got < 1 {
		t.Errorf("dependency error metric = %v", got)
	}
}
