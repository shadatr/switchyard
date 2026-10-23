package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/store"
)

type fakeLookup struct {
	mu      sync.Mutex
	keys    map[string]*store.Key // hex(hash) -> key
	calls   atomic.Int64
	fail    error
	touched []string
}

func (f *fakeLookup) KeyByHash(_ context.Context, h []byte) (*store.Key, error) {
	f.calls.Add(1)
	if f.fail != nil {
		return nil, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if k, ok := f.keys[string(h)]; ok {
		return k, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeLookup) TouchKeys(_ context.Context, ids []string) error {
	f.mu.Lock()
	f.touched = append(f.touched, ids...)
	f.mu.Unlock()
	return nil
}

func setup(t *testing.T) (*Authenticator, *fakeLookup, string, *store.Key) {
	t.Helper()
	plain, prefix, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "sy-") || len(plain) != 43 || !strings.HasPrefix(plain, prefix) {
		t.Fatalf("bad key %q prefix %q", plain, prefix)
	}
	k := &store.Key{ID: "k1", ProjectID: "p1", Name: "test"}
	f := &fakeLookup{keys: map[string]*store.Key{string(hash): k}}
	opts := DefaultOptions()
	opts.TouchEvery = 10 * time.Millisecond
	return New(f, opts, nil), f, plain, k
}

func TestValidKeyIsCached(t *testing.T) {
	a, f, plain, want := setup(t)
	for range 100 {
		k, err := a.Authenticate(context.Background(), plain)
		if err != nil || k != want {
			t.Fatalf("got %v %v", k, err)
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("store called %d times, want 1 (cache)", n)
	}
}

func TestUnknownKeyNegativeCache(t *testing.T) {
	a, f, _, _ := setup(t)
	other, _, _, _ := Generate()
	for range 50 {
		if _, err := a.Authenticate(context.Background(), other); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("store called %d times for an unknown key, want 1 (negative cache)", n)
	}
}

func TestMalformedKeyNeverHitsStore(t *testing.T) {
	a, f, _, _ := setup(t)
	for _, k := range []string{"", "sk-abc", "sy-short", strings.Repeat("x", 43)} {
		if _, err := a.Authenticate(context.Background(), k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%q: err = %v", k, err)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("malformed keys must not reach the store")
	}
}

func TestDisabledKey(t *testing.T) {
	a, _, plain, k := setup(t)
	k.Disabled = true
	if _, err := a.Authenticate(context.Background(), plain); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreDownServesCachedKeys(t *testing.T) {
	a, f, plain, _ := setup(t)
	if _, err := a.Authenticate(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	f.fail = errors.New("connection refused")
	if _, err := a.Authenticate(context.Background(), plain); err != nil {
		t.Fatalf("cached key should still work: %v", err)
	}
	other, _, _, _ := Generate()
	if _, err := a.Authenticate(context.Background(), other); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("uncached key with store down: err = %v, want ErrUnavailable", err)
	}
}

func TestTouchFlushesBatched(t *testing.T) {
	a, f, plain, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.RunTouch(ctx); close(done) }()
	for range 10 {
		_, _ = a.Authenticate(context.Background(), plain)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.touched) != 1 || f.touched[0] != "k1" {
		t.Fatalf("touched = %v, want one batched update for k1", f.touched)
	}
}

func TestGenerateUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		p, _, h, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] || len(h) != 32 || bytes.Equal(h, make([]byte, 32)) {
			t.Fatal("duplicate or bad key")
		}
		seen[p] = true
	}
}
