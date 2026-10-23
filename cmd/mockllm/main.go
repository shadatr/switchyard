// Command mockllm runs a fake OpenAI-compatible upstream for tests and
// benchmarks. See PLAN.md §7.
package main

import (
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/shadatr/switchyard/internal/mockllm"
)

func main() {
	d := mockllm.DefaultOptions()
	addr := flag.String("addr", ":9797", "listen address")
	ttft := flag.Duration("ttft", d.TTFT, "time to first token")
	itl := flag.Duration("itl", d.ITL, "inter-token latency")
	tokens := flag.Int("tokens", d.Tokens, "completion tokens per response")
	errorRate := flag.Float64("error-rate", 0, "probability of a failed request (0-1)")
	errorStatus := flag.Int("error-status", d.ErrorStatus, "HTTP status for random failures")
	jitter := flag.Float64("jitter", 0, "±fraction of jitter applied to ttft and itl")
	flag.Parse()

	srv := mockllm.New(mockllm.Options{
		TTFT: *ttft, ITL: *itl, Tokens: *tokens,
		ErrorRate: *errorRate, ErrorStatus: *errorStatus, Jitter: *jitter,
	})
	slog.Info("mockllm listening", "addr", *addr, "ttft", ttft.String(), "itl", itl.String(), "tokens", *tokens, "error_rate", *errorRate)
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("mockllm", "err", err)
		os.Exit(1)
	}
}
