//go:build integration

package admin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/shadatr/switchyard/internal/logsink"
)

// Run with: SWITCHYARD_CH_PASSWORD=... go test -tags integration ./internal/admin
func analytics(t *testing.T) *Analytics {
	t.Helper()
	ctx := context.Background()
	conn, err := logsink.Open(ctx, "127.0.0.1:19000", "switchyard", "default", os.Getenv("SWITCHYARD_CH_PASSWORD"))
	if err != nil {
		t.Skip("clickhouse not available:", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := logsink.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return &Analytics{Conn: conn}
}

func TestAnalyticsEndToEnd(t *testing.T) {
	a := analytics(t)
	ctx := context.Background()
	project := "it-" + ulid.Make().String() // isolates this run's rows
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Millisecond)
	var rows []logsink.Record
	for i := range 120 {
		status := uint16(200)
		if i%10 == 0 {
			status = 502
		}
		cache := "miss"
		if i%4 == 0 {
			cache = "hit"
		}
		rows = append(rows, logsink.Record{TS: base.Add(time.Duration(i) * time.Second), RequestID: ulid.Make().String(),
			ProjectID: project, KeyID: "k", ModelAlias: []string{"fast", "smart"}[i%2], Target: "mock/a", Attempts: 1,
			Status: status, Cache: cache, Stream: true, PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.001, LatencyMs: float32(100 + i), TTFTMs: 20})
	}
	if err := (logsink.CHWriter{Conn: a.Conn}).Write(ctx, rows); err != nil {
		t.Fatal(err)
	}
	f := Filter{From: base.Add(-time.Minute), To: base.Add(5 * time.Minute), Project: project}

	o, err := a.Overview(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if o.Requests != 120 || o.Errors != 12 || o.CacheHits != 30 || o.PromptTokens != 1200 || o.Source != "raw" {
		t.Fatalf("overview %+v", o)
	}
	if o.LatencyMs.P50 < 150 || o.LatencyMs.P50 > 170 {
		t.Errorf("latency p50 %v", o.LatencyMs.P50)
	}

	// The 1-minute rollup (materialized view) must agree on counts.
	fr := f
	fr.From = base.Add(-3 * time.Hour)
	ro, err := a.Overview(ctx, fr)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Source != "rollup_1m" || ro.Requests != 120 || ro.Errors != 12 || ro.CacheHits != 30 {
		t.Fatalf("rollup overview %+v", ro)
	}

	pts, err := a.Timeseries(ctx, f, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, p := range pts {
		total += p.Requests
	}
	if total != 120 || len(pts) < 2 {
		t.Fatalf("timeseries total %d over %d points", total, len(pts))
	}

	// Keyset pagination returns every row exactly once, newest first.
	seen := map[string]bool{}
	cursor := ""
	var last time.Time
	for page := 0; page < 10; page++ {
		rs, next, err := a.Requests(ctx, f, cursor, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if seen[r.RequestID] {
				t.Fatalf("duplicate row %s", r.RequestID)
			}
			if !last.IsZero() && r.TS.After(last) {
				t.Fatal("rows not in descending order")
			}
			seen[r.RequestID], last = true, r.TS
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 120 {
		t.Fatalf("paginated %d rows, want 120", len(seen))
	}

	errs := f
	errs.Status = "error"
	if rs, _, _ := a.Requests(ctx, errs, "", 100); len(rs) != 12 {
		t.Fatalf("status=error returned %d rows", len(rs))
	}
	one, err := a.Request(ctx, rows[7].RequestID)
	if err != nil || one.LatencyMs != 107 {
		t.Fatalf("request by id: %+v %v", one, err)
	}
	if _, err := a.Request(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing id err = %v", err)
	}

	g, err := a.Breakdown(ctx, f, "model")
	if err != nil || len(g) != 2 || g[0].Requests != 60 {
		t.Fatalf("breakdown %+v %v", g, err)
	}
}

func TestOverviewPerformanceAtVolume(t *testing.T) {
	a := analytics(t)
	ctx := context.Background()
	var n uint64
	_ = a.Conn.QueryRow(ctx, "SELECT count() FROM requests").Scan(&n)
	if n < 1_000_000 {
		t.Skipf("only %d rows; seed with: go run ./cmd/seedlogs -rows 1000000", n)
	}
	now := time.Now().UTC()
	f := Filter{From: now.Add(-24 * time.Hour), To: now}
	_, _ = a.Overview(ctx, f) // warm
	best := time.Hour
	for range 5 {
		start := time.Now()
		if _, err := a.Overview(ctx, f); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	t.Logf("24h overview over %d rows: best of 5 = %s", n, best)
	if best > 150*time.Millisecond {
		t.Fatalf("overview took %s, want < 150ms", best)
	}
}
