// Command switchyard runs the Switchyard LLM gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
	"github.com/shadatr/switchyard/internal/ratelimit"
	"github.com/shadatr/switchyard/internal/server"
	"github.com/shadatr/switchyard/internal/store"
)

func main() {
	_ = config.LoadDotEnv(".env")
	if len(os.Args) > 1 && os.Args[1] == "keys" {
		if err := runKeys(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("switchyard exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "deploy/switchyard.example.yaml", "path to YAML config")
	debugAddr := flag.String("debug-addr", "", "if set, serve pprof on this address (e.g. localhost:6060)")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New()
	deps := server.Deps{
		Config:    cfg,
		Providers: providers,
		Metrics:   m,
		Logger:    slog.Default(),
	}
	if cfg.Redis.Addr != "" {
		rdb := redis.NewClient(&redis.Options{
			Addr: cfg.Redis.Addr, DialTimeout: time.Second,
			ReadTimeout: cfg.Redis.Timeout, WriteTimeout: cfg.Redis.Timeout,
			PoolSize: 256, MaxRetries: -1,
		})
		defer rdb.Close()
		if err := rdb.Ping(ctx).Err(); err != nil {
			slog.Warn("redis not reachable at startup; limits will fail open until it is", "addr", cfg.Redis.Addr, "err", err)
		}
		deps.Limiter = ratelimit.New(rdb, cfg.Redis.Timeout, m, slog.Default())
	}
	if cfg.Auth.IsEnabled() {
		st, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer st.Close()
		authn := auth.New(st, auth.DefaultOptions(), slog.Default())
		go authn.RunTouch(ctx)
		deps.Auth = authn
	} else {
		slog.Warn("authentication is DISABLED (auth.enabled: false) — do not expose this instance")
	}
	srv := server.New(deps)

	hs := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
	}

	if *debugAddr != "" {
		go serveDebug(*debugAddr)
	}

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

func openStore(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	st, err := store.Open(ctx, os.Getenv(cfg.Postgres.DSNEnv))
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return st, nil
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

// serveDebug exposes pprof on a separate, opt-in listener so profiling
// endpoints never share the public port.
func serveDebug(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	slog.Info("debug server listening", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("debug server", "err", err)
	}
}
