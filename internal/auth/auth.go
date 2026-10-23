// Package auth authenticates API keys. Keys are looked up by SHA-256 hash
// and cached in memory so Postgres is not on the hot path for repeat callers.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/shadatr/switchyard/internal/store"
)

// Errors returned by Authenticate.
var (
	ErrInvalidKey  = errors.New("auth: invalid api key")
	ErrDisabled    = errors.New("auth: api key disabled")
	ErrUnavailable = errors.New("auth: key store unavailable")
)

// KeyPrefix starts every Switchyard key.
const KeyPrefix = "sy-"

const keyRandomLen = 40

// Lookup is the subset of the store the authenticator needs.
type Lookup interface {
	KeyByHash(ctx context.Context, hash []byte) (*store.Key, error)
	TouchKeys(ctx context.Context, ids []string) error
}

// Options tune caching.
type Options struct {
	CacheSize   int
	TTL         time.Duration // positive cache TTL (key changes propagate within this window)
	NegativeTTL time.Duration // how long unknown keys are remembered
	TouchEvery  time.Duration // last_used_at flush interval
}

// DefaultOptions per PLAN.md §5.1.
func DefaultOptions() Options {
	return Options{CacheSize: 10_000, TTL: 30 * time.Second, NegativeTTL: 5 * time.Second, TouchEvery: 10 * time.Second}
}

// Authenticator resolves bearer tokens to key records.
type Authenticator struct {
	lookup Lookup
	opts   Options
	log    *slog.Logger

	cache    *expirable.LRU[[32]byte, *store.Key]
	negative *expirable.LRU[[32]byte, struct{}]

	mu      sync.Mutex
	touched map[string]struct{}
}

// New builds an Authenticator.
func New(lookup Lookup, opts Options, log *slog.Logger) *Authenticator {
	if log == nil {
		log = slog.Default()
	}
	return &Authenticator{
		lookup:   lookup,
		opts:     opts,
		log:      log,
		cache:    expirable.NewLRU[[32]byte, *store.Key](opts.CacheSize, nil, opts.TTL),
		negative: expirable.NewLRU[[32]byte, struct{}](opts.CacheSize, nil, opts.NegativeTTL),
		touched:  make(map[string]struct{}),
	}
}

// Authenticate returns the key record for a plaintext key.
func (a *Authenticator) Authenticate(ctx context.Context, plaintext string) (*store.Key, error) {
	if len(plaintext) != len(KeyPrefix)+keyRandomLen || plaintext[:len(KeyPrefix)] != KeyPrefix {
		return nil, ErrInvalidKey
	}
	h := sha256.Sum256([]byte(plaintext))
	k, ok := a.cache.Get(h)
	if !ok {
		if _, neg := a.negative.Get(h); neg {
			return nil, ErrInvalidKey
		}
		var err error
		k, err = a.lookup.KeyByHash(ctx, h[:])
		switch {
		case errors.Is(err, store.ErrNotFound):
			a.negative.Add(h, struct{}{})
			return nil, ErrInvalidKey
		case err != nil:
			a.log.Warn("key lookup failed", "err", err)
			return nil, ErrUnavailable
		}
		a.cache.Add(h, k)
	}
	if k.Disabled {
		return nil, ErrDisabled
	}
	a.mu.Lock()
	a.touched[k.ID] = struct{}{}
	a.mu.Unlock()
	return k, nil
}

// Invalidate drops cached entries so admin changes apply immediately.
func (a *Authenticator) Invalidate() {
	a.cache.Purge()
	a.negative.Purge()
}

// RunTouch flushes last_used_at updates in batches until ctx is done.
func (a *Authenticator) RunTouch(ctx context.Context) {
	t := time.NewTicker(a.opts.TouchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.flushTouched(context.WithoutCancel(ctx))
			return
		case <-t.C:
			a.flushTouched(ctx)
		}
	}
}

func (a *Authenticator) flushTouched(ctx context.Context) {
	a.mu.Lock()
	if len(a.touched) == 0 {
		a.mu.Unlock()
		return
	}
	ids := make([]string, 0, len(a.touched))
	for id := range a.touched {
		ids = append(ids, id)
	}
	a.touched = make(map[string]struct{}, len(ids))
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.lookup.TouchKeys(ctx, ids); err != nil {
		a.log.Warn("update last_used_at failed", "err", err, "keys", len(ids))
	}
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Generate returns a new plaintext key, its display prefix and its hash.
func Generate() (plaintext, prefix string, hash []byte, err error) {
	b := make([]byte, keyRandomLen)
	max := big.NewInt(int64(len(base62)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", "", nil, err
		}
		b[i] = base62[n.Int64()]
	}
	plaintext = KeyPrefix + string(b)
	h := sha256.Sum256([]byte(plaintext))
	return plaintext, plaintext[:len(KeyPrefix)+5], h[:], nil
}
