// Package server wires HTTP routes, middleware and the chat pipeline.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/store"
)

// Response headers set on every chat response.
const (
	HeaderRequestID = "X-Switchyard-Request-Id"
	HeaderTarget    = "X-Switchyard-Target"
	HeaderCache     = "X-Switchyard-Cache"
	HeaderAttempts  = "X-Switchyard-Attempts"
)

// Authenticator resolves a bearer token to a key record.
type Authenticator interface {
	Authenticate(ctx context.Context, plaintext string) (*store.Key, error)
}

// Deps are the collaborators the server needs.
type Deps struct {
	Config    *config.Config
	Providers map[string]provider.Provider
	Metrics   *metrics.Metrics
	Logger    *slog.Logger
	Auth      Authenticator // nil disables authentication (benchmarks only)
}

// anonymousKey is used when authentication is disabled.
var anonymousKey = &store.Key{ID: "anonymous", ProjectID: "default", ProjectName: "default", Name: "anonymous"}

type ctxKey struct{}
type ctxStart struct{}

// timed records the arrival time before any middleware runs so overhead
// measurements include authentication.
func (s *Server) timed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxStart{}, s.now())))
	})
}

func (s *Server) startTime(ctx context.Context) time.Time {
	if t, ok := ctx.Value(ctxStart{}).(time.Time); ok {
		return t
	}
	return s.now()
}

func withKey(ctx context.Context, k *store.Key) context.Context {
	return context.WithValue(ctx, ctxKey{}, k)
}

// KeyFrom returns the authenticated key for the request.
func KeyFrom(ctx context.Context) *store.Key {
	if k, ok := ctx.Value(ctxKey{}).(*store.Key); ok {
		return k
	}
	return anonymousKey
}

// Server serves the gateway API.
type Server struct {
	cfg       *config.Config
	providers map[string]provider.Provider
	models    map[string]config.Model
	metrics   *metrics.Metrics
	log       *slog.Logger
	auth      Authenticator
	now       func() time.Time
}

// New builds a Server.
func New(d Deps) *Server {
	s := &Server{
		cfg:       d.Config,
		providers: d.Providers,
		models:    make(map[string]config.Model, len(d.Config.Models)),
		metrics:   d.Metrics,
		log:       d.Logger,
		auth:      d.Auth,
		now:       time.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.metrics == nil {
		s.metrics = metrics.New()
	}
	for _, m := range d.Config.Models {
		s.models[m.Alias] = m
	}
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /metrics", s.metrics.Handler())
	mux.Handle("POST /v1/chat/completions", s.timed(s.authenticate(http.HandlerFunc(s.handleChat))))
	mux.Handle("GET /v1/models", s.authenticate(http.HandlerFunc(s.handleModels)))
	return mux
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearer(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Missing API key. Send it as 'Authorization: Bearer sy-...'.")
			return
		}
		k, err := s.auth.Authenticate(r.Context(), token)
		switch {
		case errors.Is(err, auth.ErrUnavailable):
			writeError(w, http.StatusServiceUnavailable, "api_error", "auth_unavailable", "Authentication is temporarily unavailable.")
			return
		case errors.Is(err, auth.ErrDisabled):
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_disabled", "This API key is disabled.")
			return
		case err != nil:
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Invalid API key.")
			return
		}
		next.ServeHTTP(w, r.WithContext(withKey(r.Context(), k)))
	})
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(h[len(p):]), true
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	key := KeyFrom(r.Context())
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list"}
	out.Data = []model{}
	for _, m := range s.cfg.Models {
		if key.AllowsModel(m.Alias) {
			out.Data = append(out.Data, model{ID: m.Alias, Object: "model", OwnedBy: "switchyard"})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, typ, code, msg string) {
	writeJSON(w, status, api.ErrorBody{Error: api.ErrorDetail{Message: msg, Type: typ, Code: code}})
}
