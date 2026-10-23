// Command seedlogs writes synthetic request logs to ClickHouse so the
// analytics API and dashboard can be exercised at volume.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/logsink"
)

func main() {
	_ = config.LoadDotEnv(".env")
	addr := flag.String("addr", "127.0.0.1:19000", "ClickHouse native address")
	n := flag.Int("rows", 1_000_000, "rows to write")
	span := flag.Duration("span", 24*time.Hour, "spread rows over this period ending now")
	batch := flag.Int("batch", 100_000, "rows per insert")
	flag.Parse()

	ctx := context.Background()
	conn, err := logsink.Open(ctx, *addr, "switchyard", "default", os.Getenv("SWITCHYARD_CH_PASSWORD"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := logsink.Migrate(ctx, conn); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	w := logsink.CHWriter{Conn: conn}

	type target struct {
		alias, target string
		in, out       float64 // USD per 1M tokens
		latency       float64 // median ms
	}
	targets := []target{
		{"fast", "openai/gpt-4o-mini", 0.15, 0.60, 900},
		{"fast", "anthropic/claude-haiku-4-5", 1.00, 5.00, 1100},
		{"smart", "anthropic/claude-sonnet-4-5", 3.00, 15.00, 2600},
		{"cheap", "openrouter/llama-3.1-8b", 0.05, 0.08, 600},
	}
	projects := []string{"demo", "acme", "internal"}
	keys := []string{"key-web", "key-mobile", "key-batch", "key-eval"}
	end := time.Now().UTC()
	start := end.Add(-*span)

	rs := make([]logsink.Record, 0, *batch)
	written := 0
	t0 := time.Now()
	for i := 0; i < *n; i++ {
		// Traffic follows a daily curve.
		ts := start.Add(time.Duration(rand.Int64N(int64(*span))))
		hour := float64(ts.Hour())
		if rand.Float64() > 0.55+0.45*math.Sin((hour-6)/24*2*math.Pi) {
			ts = start.Add(time.Duration(rand.Int64N(int64(*span))))
		}
		tg := targets[rand.IntN(len(targets))]
		status, code, cacheRes := 200, "", "miss"
		switch r := rand.Float64(); {
		case r < 0.012:
			status, code = 502, "upstream_error"
		case r < 0.02:
			status, code = 429, "rate_limit_exceeded"
		case r < 0.18:
			cacheRes = "hit"
		}
		prompt := uint32(50 + rand.ExpFloat64()*400)
		completion := uint32(20 + rand.ExpFloat64()*250)
		latency := tg.latency * math.Exp(rand.NormFloat64()*0.45)
		upstream := latency - 0.3
		cost := (float64(prompt)*tg.in + float64(completion)*tg.out) / 1e6
		if cacheRes == "hit" {
			latency, upstream, cost = 0.4+rand.Float64(), 0, 0
		}
		if status != 200 {
			completion, cost = 0, 0
		}
		stream := rand.Float64() < 0.6
		ttft := latency
		if stream {
			ttft = latency * (0.15 + rand.Float64()*0.2)
		}
		rs = append(rs, logsink.Record{
			TS: ts, RequestID: ulid.Make().String(), ProjectID: projects[rand.IntN(len(projects))], KeyID: keys[rand.IntN(len(keys))],
			ModelAlias: tg.alias, Target: tg.target, Attempts: 1, Status: uint16(status), ErrorCode: code, Stream: stream,
			Cache: cacheRes, PromptTokens: prompt, CompletionTokens: completion, CostUSD: cost,
			LatencyMs: uint32(latency), TTFTMs: uint32(ttft), UpstreamMs: uint32(max(upstream, 0)), GatewayOverheadUs: uint32(80 + rand.IntN(400)),
		})
		if len(rs) == *batch || i == *n-1 {
			if err := w.Write(ctx, rs); err != nil {
				fmt.Fprintln(os.Stderr, "insert:", err)
				os.Exit(1)
			}
			written += len(rs)
			rs = rs[:0]
		}
	}
	fmt.Printf("wrote %d rows in %s\n", written, time.Since(t0).Round(time.Millisecond))
}
