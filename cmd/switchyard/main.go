// Command switchyard runs the Switchyard LLM gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/shadatr/switchyard/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("switchyard exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "deploy/switchyard.example.yaml", "path to YAML config")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, warnings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		slog.Warn("config", "warning", w)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down", "grace", cfg.Server.ShutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
