// Package server wires HTTP routes, middleware and the chat pipeline.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/provider"
)

// Response headers set on every chat response.
const (
	HeaderRequestID = "X-Switchyard-Request-Id"
	HeaderTarget    = "X-Switchyard-Target"
	HeaderCache     = "X-Switchyard-Cache"
	HeaderAttempts  = "X-Switchyard-Attempts"
)

// Deps are the collaborators the server needs.
type Deps struct {
	Config    *config.Config
	Providers map[string]provider.Provider
	Metrics   *metrics.Metrics
	Logger    *slog.Logger
	DevKey    string // temporary static API key (phase 2); empty disables auth
}

// Server serves the gateway API.
type Server struct {
	cfg       *config.Config
	providers map[string]provider.Provider
	models    map[string]config.Model
	metrics   *metrics.Metrics
	log       *slog.Logger
	devKey    []byte
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
		devKey:    []byte(d.DevKey),
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
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.handleChat)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.handleModels)))
	return mux
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.devKey) > 0 {
			key, ok := bearer(r)
			if !ok || subtle.ConstantTimeCompare([]byte(key), s.devKey) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Invalid API key.")
				return
			}
		}
		next.ServeHTTP(w, r)
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

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list"}
	for _, m := range s.cfg.Models {
		out.Data = append(out.Data, model{ID: m.Alias, Object: "model", OwnedBy: "switchyard"})
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
