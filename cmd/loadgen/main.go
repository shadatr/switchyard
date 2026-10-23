// Command loadgen is an open-model HTTP load generator for Switchyard
// benchmarks. Requests are issued at a fixed arrival rate regardless of how
// fast responses come back (avoiding coordinated omission); latency is
// measured from each request's scheduled start time.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Label        string   `json:"label"`
	URL          string   `json:"url"`
	Stream       bool     `json:"stream"`
	TargetRPS    int      `json:"target_rps"`
	DurationS    float64  `json:"duration_s"`
	Sent         int64    `json:"sent"`
	OK           int64    `json:"ok"`
	Errors       int64    `json:"errors"`
	Skipped      int64    `json:"skipped"` // concurrency cap reached; request never sent
	AchievedRPS  float64  `json:"achieved_rps"`
	LatencyMs    pct      `json:"latency_ms"`
	TTFTMs       *pct     `json:"ttft_ms,omitempty"`
	MaxInflight  int64    `json:"max_inflight"`
	ErrorSamples []string `json:"error_samples,omitempty"`
}

type pct struct {
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
	P99  float64 `json:"p99"`
	P999 float64 `json:"p999"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8787/v1/chat/completions", "target URL")
	rps := flag.Int("rps", 1000, "requests per second")
	dur := flag.Duration("duration", 30*time.Second, "measurement duration")
	warmup := flag.Duration("warmup", 5*time.Second, "warmup duration (not recorded)")
	stream := flag.Bool("stream", false, "send stream:true requests")
	model := flag.String("model", "mock-fast", "model field")
	key := flag.String("key", "", "bearer token")
	maxInflight := flag.Int("max-inflight", 20000, "concurrency cap")
	label := flag.String("label", "", "label stored in the result")
	temperature := flag.Float64("temperature", 0.7, "temperature field (0 makes requests cacheable)")
	out := flag.String("out", "", "write JSON result to this file")
	flag.Parse()

	body, _ := json.Marshal(map[string]any{
		"model":       *model,
		"stream":      *stream,
		"temperature": *temperature,
		"messages":    []map[string]string{{"role": "user", "content": "Benchmark prompt: say something short."}},
	})

	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        *maxInflight,
		MaxIdleConnsPerHost: *maxInflight,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}

	if *warmup > 0 {
		run(client, *url, *key, body, *stream, *rps, *warmup, *maxInflight, nil)
	}
	rec := &recorder{}
	start := time.Now()
	r := run(client, *url, *key, body, *stream, *rps, *dur, *maxInflight, rec)
	elapsed := time.Since(start).Seconds()

	r.Label, r.URL, r.Stream, r.TargetRPS, r.DurationS = *label, *url, *stream, *rps, dur.Seconds()
	r.AchievedRPS = float64(r.OK) / elapsed
	r.LatencyMs = rec.latency.summary()
	if *stream {
		t := rec.ttft.summary()
		r.TTFTMs = &t
	}
	r.ErrorSamples = rec.errSamples

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if *out != "" {
		b, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

type samples struct {
	mu sync.Mutex
	v  []float64
}

func (s *samples) add(d time.Duration) {
	s.mu.Lock()
	s.v = append(s.v, float64(d.Microseconds())/1000)
	s.mu.Unlock()
}

func (s *samples) summary() pct {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.v) == 0 {
		return pct{}
	}
	slices.Sort(s.v)
	q := func(p float64) float64 { return s.v[min(len(s.v)-1, int(p*float64(len(s.v))))] }
	sum := 0.0
	for _, x := range s.v {
		sum += x
	}
	return pct{P50: q(0.50), P90: q(0.90), P99: q(0.99), P999: q(0.999), Max: s.v[len(s.v)-1], Mean: sum / float64(len(s.v))}
}

type recorder struct {
	latency, ttft samples
	errMu         sync.Mutex
	errSamples    []string
}

func (r *recorder) err(msg string) {
	r.errMu.Lock()
	if len(r.errSamples) < 5 {
		r.errSamples = append(r.errSamples, msg)
	}
	r.errMu.Unlock()
}

func run(client *http.Client, url, key string, body []byte, stream bool, rps int, dur time.Duration, maxInflight int, rec *recorder) result {
	var res result
	var sent, ok, errs, skipped, inflight, maxSeen atomic.Int64
	var wg sync.WaitGroup
	interval := time.Second / time.Duration(rps)
	total := int(dur / interval)
	start := time.Now()

	for i := 0; i < total; i++ {
		scheduled := start.Add(time.Duration(i) * interval)
		if d := time.Until(scheduled); d > 0 {
			time.Sleep(d)
		}
		if inflight.Load() >= int64(maxInflight) {
			skipped.Add(1)
			continue
		}
		n := inflight.Add(1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		sent.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer inflight.Add(-1)
			lat, ttft, err := once(client, url, key, body, stream, scheduled)
			if err != nil {
				errs.Add(1)
				if rec != nil {
					rec.err(err.Error())
				}
				return
			}
			ok.Add(1)
			if rec != nil {
				rec.latency.add(lat)
				if stream {
					rec.ttft.add(ttft)
				}
			}
		}()
	}
	wg.Wait()
	res.Sent, res.OK, res.Errors, res.Skipped, res.MaxInflight = sent.Load(), ok.Load(), errs.Load(), skipped.Load(), maxSeen.Load()
	return res
}

func once(client *http.Client, url, key string, body []byte, stream bool, scheduled time.Time) (lat, ttft time.Duration, err error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return 0, 0, fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	if !stream {
		_, err = io.Copy(io.Discard, resp.Body)
		return time.Since(scheduled), 0, err
	}
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadSlice('\n')
		if ttft == 0 && bytes.Contains(line, []byte(`"content"`)) {
			ttft = time.Since(scheduled)
		}
		if bytes.HasPrefix(line, []byte("data: [DONE]")) {
			return time.Since(scheduled), ttft, nil
		}
		if err != nil {
			if err == io.EOF {
				return 0, 0, fmt.Errorf("stream ended without [DONE]")
			}
			return 0, 0, err
		}
	}
}
