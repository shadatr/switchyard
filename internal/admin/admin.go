// Package admin serves the dashboard API (/admin/api/*) and the live tail
// WebSocket (/admin/live). Every route requires the admin token.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/breaker"
	"github.com/shadatr/switchyard/internal/live"
	"github.com/shadatr/switchyard/internal/store"
)

// KeyStore is the subset of the store the admin API needs.
type KeyStore interface {
	ListKeys(ctx context.Context) ([]*store.Key, error)
	KeyByID(ctx context.Context, id string) (*store.Key, error)
	CreateKey(ctx context.Context, nk store.NewKey) (*store.Key, error)
	UpdateKey(ctx context.Context, id string, u store.KeyUpdate) (*store.Key, error)
}

// Deps are the admin API's collaborators. Nil fields disable the
// corresponding endpoints (503).
type Deps struct {
	Token          string
	Analytics      *Analytics
	Keys           KeyStore
	Spend          func(ctx context.Context, keyID string) (float64, error)
	InvalidateAuth func()
	Breakers       func() map[string]breaker.State
	Hub            *live.Hub
	CORSOrigins    []string
	Logger         *slog.Logger
}

type api struct {
	Deps
}

// Handler returns the admin handler; mount it at "/admin/".
func Handler(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	a := &api{d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/api/overview", a.overview)
	mux.HandleFunc("GET /admin/api/timeseries", a.timeseries)
	mux.HandleFunc("GET /admin/api/requests", a.requests)
	mux.HandleFunc("GET /admin/api/requests/{id}", a.request)
	mux.HandleFunc("GET /admin/api/breakdown", a.breakdown)
	mux.HandleFunc("GET /admin/api/keys", a.listKeys)
	mux.HandleFunc("POST /admin/api/keys", a.createKey)
	mux.HandleFunc("PATCH /admin/api/keys/{id}", a.updateKey)
	mux.HandleFunc("GET /admin/api/targets", a.targets)
	mux.HandleFunc("GET /admin/live", a.liveTail)
	return a.cors(a.authorize(mux))
}

func (a *api) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && a.originAllowed(o) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) originAllowed(o string) bool {
	for _, allowed := range a.CORSOrigins {
		if allowed == "*" || strings.EqualFold(allowed, o) {
			return true
		}
	}
	return false
}

func (a *api) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Token == "" {
			writeErr(w, http.StatusServiceUnavailable, "admin API disabled: set the admin token environment variable")
			return
		}
		tok := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok = strings.TrimPrefix(h, "Bearer ")
		} else if r.URL.Path == "/admin/live" {
			tok = r.URL.Query().Get("token") // browsers can't set headers on WebSockets
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(a.Token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	now := time.Now().UTC()
	f := Filter{From: now.Add(-time.Hour), To: now, Project: q.Get("project"), Model: q.Get("model"), Key: q.Get("key"), Status: q.Get("status")}
	var err error
	if v := q.Get("from"); v != "" {
		if f.From, err = parseTime(v); err != nil {
			return f, err
		}
	}
	if v := q.Get("to"); v != "" {
		if f.To, err = parseTime(v); err != nil {
			return f, err
		}
	}
	if !f.From.Before(f.To) {
		return f, errors.New("from must be before to")
	}
	return f, nil
}

// parseTime accepts RFC 3339 or unix milliseconds.
func parseTime(v string) (time.Time, error) {
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, errors.New("time must be RFC 3339 or unix milliseconds")
	}
	return t.UTC(), nil
}

func (a *api) needAnalytics(w http.ResponseWriter) bool {
	if a.Analytics == nil {
		writeErr(w, http.StatusServiceUnavailable, "analytics (ClickHouse) is not configured")
		return false
	}
	return true
}

func (a *api) overview(w http.ResponseWriter, r *http.Request) {
	if !a.needAnalytics(w) {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	o, err := a.Analytics.Overview(r.Context(), f)
	a.respond(w, o, err)
}

func (a *api) timeseries(w http.ResponseWriter, r *http.Request) {
	if !a.needAnalytics(w) {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	iv := AutoInterval(f.To.Sub(f.From))
	if v := r.URL.Query().Get("interval"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			writeErr(w, http.StatusBadRequest, "interval must be a duration >= 1s, e.g. 1m")
			return
		}
		iv = d
	}
	pts, err := a.Analytics.Timeseries(r.Context(), f, iv)
	a.respond(w, map[string]any{"interval_seconds": int(iv.Seconds()), "points": pts}, err)
}

func (a *api) requests(w http.ResponseWriter, r *http.Request) {
	if !a.needAnalytics(w) {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, next, err := a.Analytics.Requests(r.Context(), f, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, ErrBadCursor) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.respond(w, map[string]any{"data": rows, "next_cursor": next}, err)
}

func (a *api) request(w http.ResponseWriter, r *http.Request) {
	if !a.needAnalytics(w) {
		return
	}
	req, err := a.Analytics.Request(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "request not found")
		return
	}
	a.respond(w, req, err)
}

func (a *api) breakdown(w http.ResponseWriter, r *http.Request) {
	if !a.needAnalytics(w) {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "model"
	}
	g, err := a.Analytics.Breakdown(r.Context(), f, by)
	if errors.Is(err, ErrBadDimension) {
		writeErr(w, http.StatusBadRequest, "by must be one of model, key, target, status, project, cache")
		return
	}
	a.respond(w, map[string]any{"by": by, "groups": g}, err)
}

// keyView is the API representation of a key (never includes the hash).
type keyView struct {
	ID               string     `json:"id"`
	ProjectID        string     `json:"project_id"`
	Project          string     `json:"project"`
	Name             string     `json:"name"`
	Prefix           string     `json:"prefix"`
	AllowedModels    []string   `json:"allowed_models"`
	RPMLimit         *int       `json:"rpm_limit"`
	TPMLimit         *int       `json:"tpm_limit"`
	MonthlyBudgetUSD *float64   `json:"monthly_budget_usd"`
	SpentUSD         *float64   `json:"spent_this_month_usd"`
	Disabled         bool       `json:"disabled"`
	CreatedAt        time.Time  `json:"created_at"`
	LastUsedAt       *time.Time `json:"last_used_at"`
}

func (a *api) view(ctx context.Context, k *store.Key) keyView {
	v := keyView{ID: k.ID, ProjectID: k.ProjectID, Project: k.ProjectName, Name: k.Name, Prefix: k.Prefix,
		AllowedModels: k.AllowedModels, RPMLimit: k.RPMLimit, TPMLimit: k.TPMLimit, MonthlyBudgetUSD: k.MonthlyBudgetUSD,
		Disabled: k.Disabled, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
	if v.AllowedModels == nil {
		v.AllowedModels = []string{}
	}
	if a.Spend != nil {
		if s, err := a.Spend(ctx, k.ID); err == nil {
			v.SpentUSD = &s
		}
	}
	return v
}

func (a *api) listKeys(w http.ResponseWriter, r *http.Request) {
	if a.Keys == nil {
		writeErr(w, http.StatusServiceUnavailable, "key store (Postgres) is not configured")
		return
	}
	keys, err := a.Keys.ListKeys(r.Context())
	if err != nil {
		a.respond(w, nil, err)
		return
	}
	out := make([]keyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, a.view(r.Context(), k))
	}
	a.respond(w, map[string]any{"data": out}, nil)
}

type keyInput struct {
	Project          *string   `json:"project"`
	Name             *string   `json:"name"`
	AllowedModels    *[]string `json:"allowed_models"`
	RPMLimit         *int      `json:"rpm_limit"`
	TPMLimit         *int      `json:"tpm_limit"`
	MonthlyBudgetUSD *float64  `json:"monthly_budget_usd"`
	Disabled         *bool     `json:"disabled"`
	// Explicit nulls clear limits on PATCH.
	ClearRPM    bool `json:"clear_rpm_limit"`
	ClearTPM    bool `json:"clear_tpm_limit"`
	ClearBudget bool `json:"clear_monthly_budget"`
}

func (in keyInput) validate() error {
	if in.RPMLimit != nil && *in.RPMLimit <= 0 || in.TPMLimit != nil && *in.TPMLimit <= 0 {
		return errors.New("limits must be positive")
	}
	if in.MonthlyBudgetUSD != nil && *in.MonthlyBudgetUSD < 0 {
		return errors.New("budget must be >= 0")
	}
	return nil
}

func (a *api) createKey(w http.ResponseWriter, r *http.Request) {
	if a.Keys == nil {
		writeErr(w, http.StatusServiceUnavailable, "key store (Postgres) is not configured")
		return
	}
	var in keyInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	project := "default"
	if in.Project != nil && *in.Project != "" {
		project = *in.Project
	}
	plain, prefix, hash, err := auth.Generate()
	if err != nil {
		a.respond(w, nil, err)
		return
	}
	nk := store.NewKey{ProjectName: project, Name: strings.TrimSpace(*in.Name), Prefix: prefix, Hash: hash,
		RPMLimit: in.RPMLimit, TPMLimit: in.TPMLimit, MonthlyBudgetUSD: in.MonthlyBudgetUSD}
	if in.AllowedModels != nil {
		nk.AllowedModels = *in.AllowedModels
	}
	k, err := a.Keys.CreateKey(r.Context(), nk)
	if err != nil {
		a.respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"key": a.view(r.Context(), k), "secret": plain})
}

func (a *api) updateKey(w http.ResponseWriter, r *http.Request) {
	if a.Keys == nil {
		writeErr(w, http.StatusServiceUnavailable, "key store (Postgres) is not configured")
		return
	}
	var in keyInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := store.KeyUpdate{Name: in.Name, AllowedModels: in.AllowedModels, Disabled: in.Disabled}
	setInt := func(v *int, clear bool) **int {
		if clear {
			var none *int
			return &none
		}
		if v != nil {
			return &v
		}
		return nil
	}
	u.RPMLimit, u.TPMLimit = setInt(in.RPMLimit, in.ClearRPM), setInt(in.TPMLimit, in.ClearTPM)
	if in.ClearBudget {
		var none *float64
		u.MonthlyBudgetUSD = &none
	} else if in.MonthlyBudgetUSD != nil {
		u.MonthlyBudgetUSD = &in.MonthlyBudgetUSD
	}
	k, err := a.Keys.UpdateKey(r.Context(), r.PathValue("id"), u)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "key not found")
		return
	}
	if err == nil && a.InvalidateAuth != nil {
		a.InvalidateAuth() // apply limit/disable changes immediately on this instance
	}
	if err != nil {
		a.respond(w, nil, err)
		return
	}
	a.respond(w, a.view(r.Context(), k), nil)
}

type targetView struct {
	Target       string  `json:"target"`
	State        string  `json:"state"`
	Requests     uint64  `json:"requests_15m"`
	Errors       uint64  `json:"errors_15m"`
	LatencyP95Ms float64 `json:"latency_p95_ms_15m"`
}

func (a *api) targets(w http.ResponseWriter, r *http.Request) {
	states := map[string]breaker.State{}
	if a.Breakers != nil {
		states = a.Breakers()
	}
	stats := map[string]Group{}
	if a.Analytics != nil {
		now := time.Now().UTC()
		if groups, err := a.Analytics.Breakdown(r.Context(), Filter{From: now.Add(-15 * time.Minute), To: now}, "target"); err == nil {
			for _, g := range groups {
				stats[g.Key] = g
			}
		}
	}
	out := make([]targetView, 0, len(states))
	for t, st := range states {
		g := stats[t]
		out = append(out, targetView{Target: t, State: st.String(), Requests: g.Requests, Errors: g.Errors, LatencyP95Ms: g.LatencyP95Ms})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	a.respond(w, map[string]any{"data": out}, nil)
}

// liveTail streams batches of request events every 250 ms.
func (a *api) liveTail(w http.ResponseWriter, r *http.Request) {
	if a.Hub == nil {
		writeErr(w, http.StatusServiceUnavailable, "live tail is not available")
		return
	}
	patterns := []string{}
	for _, o := range a.CORSOrigins {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			patterns = append(patterns, u.Host)
		} else if o == "*" {
			patterns = append(patterns, "*")
		}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context()) // we only write; reading handles pings/close
	events, dropped, cancel := a.Hub.Subscribe()
	defer cancel()

	const maxBatch = 200
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	batch := make([]live.Event, 0, maxBatch)
	skipped := int64(0)     // events beyond maxBatch in the current window
	lastDropped := int64(0) // hub drops already reported
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			if len(batch) < maxBatch {
				batch = append(batch, e)
			} else {
				skipped++
			}
		case <-tick.C:
			d := dropped()
			newDrops := d - lastDropped
			if len(batch) == 0 && skipped == 0 && newDrops == 0 {
				continue
			}
			lastDropped = d
			// skipped is per batch: events this subscriber missed since the last message.
			msg, _ := json.Marshal(map[string]any{"events": batch, "skipped": skipped + newDrops})
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
			batch, skipped = batch[:0], 0
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}

func (a *api) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		a.Logger.Error("admin query failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}
