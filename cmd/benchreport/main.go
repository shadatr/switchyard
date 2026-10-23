// Command benchreport turns a bench/results/<run> directory into a Markdown
// table: client-observed latency (baseline vs gateway) and the gateway's
// self-measured overhead from its Prometheus histogram.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type pct struct {
	P50, P90, P99, P999, Max, Mean float64
}

type result struct {
	Label       string  `json:"label"`
	Stream      bool    `json:"stream"`
	TargetRPS   int     `json:"target_rps"`
	Sent        int64   `json:"sent"`
	OK          int64   `json:"ok"`
	Errors      int64   `json:"errors"`
	Skipped     int64   `json:"skipped"`
	AchievedRPS float64 `json:"achieved_rps"`
	LatencyMs   pct     `json:"latency_ms"`
	TTFTMs      *pct    `json:"ttft_ms"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: benchreport <results-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	load := func(name string) (*result, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var r result
		return &r, json.Unmarshal(b, &r)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "gateway-*.json"))
	type row struct {
		mode string
		rps  int
		b, g *result
		ovh  [3]float64
	}
	var rows []row
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".json") // gateway-<mode>-<rps>
		parts := strings.Split(base, "-")
		mode := parts[1]
		rps, _ := strconv.Atoi(parts[2])
		g, err := load(filepath.Base(f))
		if err != nil {
			fail(err)
		}
		b, err := load(fmt.Sprintf("baseline-%s-%d.json", mode, rps))
		if err != nil {
			fail(err)
		}
		buckets, err := histogram(filepath.Join(dir, base+".metrics"), "switchyard_gateway_overhead_seconds")
		if err != nil {
			fail(err)
		}
		rows = append(rows, row{mode: mode, rps: rps, b: b, g: g,
			ovh: [3]float64{quantile(buckets, 0.5) * 1000, quantile(buckets, 0.95) * 1000, quantile(buckets, 0.99) * 1000}})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].mode != rows[j].mode {
			return rows[i].mode < rows[j].mode // nonstream first
		}
		return rows[i].rps < rows[j].rps
	})

	fmt.Printf("Run: `%s` · GOMAXPROCS=%d · %s/%s\n\n", filepath.Base(dir), runtime.GOMAXPROCS(0), runtime.GOOS, runtime.GOARCH)
	fmt.Println("| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Gateway self-measured overhead p50 / p95 / p99 (ms) |")
	fmt.Println("|---|---:|---:|---:|---|---|---|---|")
	for _, r := range rows {
		fmt.Printf("| %s | %d | %.0f | %d | %.2f / %.2f | %.2f / %.2f | %+.2f / %+.2f | %.3f / %.3f / %.3f |\n",
			r.mode, r.rps, r.g.AchievedRPS, r.g.Errors+r.g.Skipped,
			r.b.LatencyMs.P50, r.b.LatencyMs.P99, r.g.LatencyMs.P50, r.g.LatencyMs.P99,
			r.g.LatencyMs.P50-r.b.LatencyMs.P50, r.g.LatencyMs.P99-r.b.LatencyMs.P99,
			r.ovh[0], r.ovh[1], r.ovh[2])
	}
	fmt.Println()
	for _, r := range rows {
		if r.g.TTFTMs != nil && r.b.TTFTMs != nil {
			fmt.Printf("- stream @ %d rps: TTFT p50 baseline %.2f ms → gateway %.2f ms; p99 %.2f → %.2f ms\n",
				r.rps, r.b.TTFTMs.P50, r.g.TTFTMs.P50, r.b.TTFTMs.P99, r.g.TTFTMs.P99)
		}
	}
}

type bucket struct {
	le    float64
	count float64
}

// histogram parses cumulative buckets of a Prometheus histogram.
func histogram(path, name string) ([]bucket, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []bucket
	sc := bufio.NewScanner(f)
	prefix := name + `_bucket{le="`
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := line[len(prefix):]
		i := strings.Index(rest, `"}`)
		leStr, valStr := rest[:i], strings.TrimSpace(rest[i+2:])
		le := 1e18
		if leStr != "+Inf" {
			le, _ = strconv.ParseFloat(leStr, 64)
		}
		v, _ := strconv.ParseFloat(valStr, 64)
		out = append(out, bucket{le, v})
	}
	return out, sc.Err()
}

// quantile mirrors Prometheus histogram_quantile (linear interpolation).
func quantile(b []bucket, q float64) float64 {
	if len(b) == 0 || b[len(b)-1].count == 0 {
		return 0
	}
	rank := q * b[len(b)-1].count
	prevLe, prevCount := 0.0, 0.0
	for _, x := range b {
		if x.count >= rank {
			if x.le == 1e18 {
				return prevLe
			}
			if x.count == prevCount {
				return x.le
			}
			return prevLe + (x.le-prevLe)*(rank-prevCount)/(x.count-prevCount)
		}
		prevLe, prevCount = x.le, x.count
	}
	return prevLe
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "benchreport:", err)
	os.Exit(1)
}
