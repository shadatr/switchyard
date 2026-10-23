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
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/admin"
	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/cache"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/live"
	"github.com/shadatr/switchyard/internal/logsink"
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
	var background sync.WaitGroup // goroutines that must finish before exit (log drain)

	m := metrics.New()
	hub := live.NewHub(m)
	deps := server.Deps{Config: cfg, Providers: providers, Metrics: m, Logger: slog.Default(), Live: hub}
	adm := admin.Deps{Token: os.Getenv(cfg.Admin.TokenEnv), Hub: hub, CORSOrigins: cfg.Admin.CORSOrigins, Logger: slog.Default()}

	// Redis: rate limits, budgets, cache (all fail open).
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
		limiter := ratelimit.New(rdb, cfg.Redis.Timeout, m, slog.Default())
		deps.Limiter = limiter
		adm.Spend = limiter.Spent
		if cfg.Cache.Enabled {
			deps.Cache = cache.New(rdb, cfg.Cache, cfg.Redis.Timeout, m)
		}
	}

	// Postgres: API keys (required when auth is enabled).
	if cfg.Auth.IsEnabled() {
		st, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer st.Close()
		authn := auth.New(st, auth.DefaultOptions(), slog.Default())
		go authn.RunTouch(ctx)
		deps.Auth = authn
		adm.Keys = st
		adm.InvalidateAuth = authn.Invalidate
	} else {
		slog.Warn("authentication is DISABLED (auth.enabled: false) — do not expose this instance")
	}

	// ClickHouse: request analytics (optional, never on the hot path).
	if cfg.ClickHouse.Addr != "" {
		conn, err := logsink.Open(ctx, cfg.ClickHouse.Addr, cfg.ClickHouse.Database, cfg.ClickHouse.Username, os.Getenv(cfg.ClickHouse.PasswordEnv))
		if err != nil {
			slog.Warn("clickhouse not reachable at startup; analytics disabled", "addr", cfg.ClickHouse.Addr, "err", err)
		} else {
			defer conn.Close()
			if err := logsink.Migrate(ctx, conn); err != nil {
				return fmt.Errorf("migrate clickhouse: %w", err)
			}
			sink := logsink.New(logsink.CHWriter{Conn: conn}, logsink.Options{
				QueueSize: cfg.LogSink.QueueSize, BatchSize: cfg.LogSink.BatchSize, FlushInterval: cfg.LogSink.FlushInterval,
			}, m, slog.Default())
			background.Add(1)
			go func() { defer background.Done(); sink.Run(ctx) }()
			deps.Sink = sink
			adm.Analytics = &admin.Analytics{Conn: conn}
		}
	}

	srv := server.New(deps)
	adm.Breakers = srv.Router().BreakerStates
	if adm.Token == "" {
		slog.Warn("admin API disabled: set " + cfg.Admin.TokenEnv)
	}

	root := http.NewServeMux()
	root.Handle("/admin/", admin.Handler(adm))
	root.Handle("/", srv.Handler())
	hs := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           root,
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
	err = hs.Shutdown(shutdownCtx)
	background.Wait() // let the log sink drain
	return err
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
