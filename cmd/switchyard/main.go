// Command switchyard runs the Switchyard LLM gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
	"github.com/shadatr/switchyard/internal/server"
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

	providers, err := buildProviders(cfg)
	if err != nil {
		return err
	}

	srv := server.New(server.Deps{
		Config:    cfg,
		Providers: providers,
		Metrics:   metrics.New(),
		Logger:    slog.Default(),
		DevKey:    os.Getenv("SWITCHYARD_DEV_KEY"),
	})

	hs := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Server.Addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	return hs.Shutdown(shutdownCtx)
}

func buildProviders(cfg *config.Config) (map[string]provider.Provider, error) {
	out := make(map[string]provider.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		key := ""
		if p.APIKeyEnv != "" {
			key = os.Getenv(p.APIKeyEnv)
		}
		switch p.Type {
		case config.ProviderOpenAI, config.ProviderMock:
			out[p.Name] = openai.New(p, key)
		default:
			return nil, fmt.Errorf("provider %q: type %q is not implemented yet", p.Name, p.Type)
		}
	}
	return out, nil
}
