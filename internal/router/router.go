// Package router decides which targets serve a model alias, in what order,
// and whether a failed attempt should be retried.
package router

import (
	"context"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/shadatr/switchyard/internal/breaker"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/provider"
)

// Router is safe for concurrent use.
type Router struct {
	cfg      config.Routing
	models   map[string]config.Model
	breakers map[string]*breaker.Breaker // by target key

	retryStatus map[int]bool
	retryCodes  map[string]bool

	rngMu sync.Mutex
	rng   *rand.Rand
}

// New builds a Router with one breaker per distinct target.
func New(cfg *config.Config, m *metrics.Metrics) *Router {
	r := &Router{
		cfg:         cfg.Routing,
		models:      make(map[string]config.Model, len(cfg.Models)),
		breakers:    make(map[string]*breaker.Breaker),
		retryStatus: make(map[int]bool),
		retryCodes:  make(map[string]bool),
		rng:         rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
	for _, s := range cfg.Routing.RetryOn {
		if n, err := strconv.Atoi(s); err == nil {
			r.retryStatus[n] = true
		} else {
			r.retryCodes[s] = true
		}
	}
	for _, md := range cfg.Models {
		r.models[md.Alias] = md
		for _, t := range md.Targets {
			key := t.Key()
			if _, ok := r.breakers[key]; ok {
				continue
			}
			var onChange func(breaker.State)
			if m != nil {
				g := m.BreakerState.WithLabelValues(key)
				g.Set(0)
				onChange = func(s breaker.State) { g.Set(float64(s)) }
			}
			r.breakers[key] = breaker.New(breaker.Options{
				FailureRate:         cfg.Breaker.FailureRate,
				MinRequests:         cfg.Breaker.MinRequests,
				Window:              cfg.Breaker.Window,
				OpenDuration:        cfg.Breaker.OpenDuration,
				HalfOpenMaxRequests: cfg.Breaker.HalfOpenMaxRequests,
				OnChange:            onChange,
			})
		}
	}
	return r
}

// Model returns the alias config.
func (r *Router) Model(alias string) (config.Model, bool) {
	m, ok := r.models[alias]
	return m, ok
}

// MaxAttempts is the configured attempt budget per request.
func (r *Router) MaxAttempts() int { return r.cfg.MaxAttempts }

// FirstByteTimeout bounds how long a stream may take to produce its first chunk.
func (r *Router) FirstByteTimeout() time.Duration { return r.cfg.FirstByteTimeout }

// Breaker returns the breaker for a target.
func (r *Router) Breaker(t config.Target) *breaker.Breaker { return r.breakers[t.Key()] }

// BreakerByKey returns the breaker for a "provider/model" key, or a
// throwaway breaker if the key is unknown.
func (r *Router) BreakerByKey(key string) *breaker.Breaker {
	if b, ok := r.breakers[key]; ok {
		return b
	}
	return breaker.New(breaker.Options{})
}

// BreakerStates reports every target's state (for the admin API).
func (r *Router) BreakerStates() map[string]breaker.State {
	out := make(map[string]breaker.State, len(r.breakers))
	for k, b := range r.breakers {
		out[k] = b.State()
	}
	return out
}

// Plan returns the order in which targets should be tried.
// priority: as configured; weighted: weighted random without replacement.
func (r *Router) Plan(m config.Model) []config.Target {
	if m.Strategy != config.StrategyWeighted || len(m.Targets) == 1 {
		return m.Targets
	}
	pool := append([]config.Target(nil), m.Targets...)
	out := make([]config.Target, 0, len(pool))
	r.rngMu.Lock()
	defer r.rngMu.Unlock()
	for len(pool) > 0 {
		total := 0
		for _, t := range pool {
			total += t.Weight
		}
		x := r.rng.IntN(total)
		i := 0
		for ; i < len(pool)-1; i++ {
			x -= pool[i].Weight
			if x < 0 {
				break
			}
		}
		out = append(out, pool[i])
		pool = append(pool[:i], pool[i+1:]...)
	}
	return out
}

// Retryable reports whether err justifies another attempt.
func (r *Router) Retryable(err error) bool {
	pe, ok := provider.AsError(err)
	if !ok || !pe.Retryable {
		return false
	}
	if pe.Status != 0 {
		return r.retryStatus[pe.Status]
	}
	switch pe.Code {
	case provider.CodeTimeout:
		return r.retryCodes["timeout"]
	case provider.CodeConnect, provider.CodeUpstream:
		return r.retryCodes["connect_error"]
	case provider.CodeBadResponse:
		return r.retryStatus[502]
	}
	return false
}

// CountsAsFailure reports whether err should count against a target's
// breaker. Client mistakes (4xx other than 408/429) and cancellations don't.
func CountsAsFailure(err error) bool {
	pe, ok := provider.AsError(err)
	if !ok {
		return true
	}
	if pe.Code == provider.CodeCanceled {
		return false
	}
	if pe.Status >= 400 && pe.Status < 500 {
		return pe.Status == 408 || pe.Status == 429
	}
	return true
}

// Backoff returns the delay before attempt n (n >= 2): exponential from
// Base, capped at Max, with "equal jitter" when enabled.
func (r *Router) Backoff(n int) time.Duration {
	b := r.cfg.RetryBackoff
	d := b.Base << max(n-2, 0)
	if d > b.Max || d <= 0 {
		d = b.Max
	}
	if b.Jitter && d > 1 {
		r.rngMu.Lock()
		j := time.Duration(r.rng.Int64N(int64(d / 2)))
		r.rngMu.Unlock()
		d = d/2 + j
	}
	return d
}

// Sleep waits for d or until ctx is done; it reports whether the wait completed.
func Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
