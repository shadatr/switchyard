//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"
)

// Run with: SWITCHYARD_PG_DSN=... go test -tags integration ./internal/store
func open(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("SWITCHYARD_PG_DSN")
	if dsn == "" {
		t.Skip("SWITCHYARD_PG_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestKeyLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	h := sha256.Sum256([]byte(t.Name() + time.Now().String()))
	rpm, budget := 60, 2.5
	k, err := s.CreateKey(ctx, NewKey{ProjectName: "it-" + t.Name(), Name: "k", Prefix: "sy-abcde", Hash: h[:],
		AllowedModels: []string{"fast"}, RPMLimit: &rpm, MonthlyBudgetUSD: &budget})
	if err != nil {
		t.Fatal(err)
	}
	if k.RPMLimit == nil || *k.RPMLimit != 60 || k.TPMLimit != nil || *k.MonthlyBudgetUSD != 2.5 || !k.AllowsModel("fast") || k.AllowsModel("slow") {
		t.Fatalf("unexpected key %+v", k)
	}
	got, err := s.KeyByHash(ctx, h[:])
	if err != nil || got.ID != k.ID {
		t.Fatalf("by hash: %v %v", got, err)
	}
	disabled := true
	var noLimit *int
	u, err := s.UpdateKey(ctx, k.ID, KeyUpdate{Disabled: &disabled, RPMLimit: &noLimit})
	if err != nil || !u.Disabled || u.RPMLimit != nil {
		t.Fatalf("update: %+v %v", u, err)
	}
	if err := s.TouchKeys(ctx, []string{k.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.KeyByID(ctx, k.ID); got.LastUsedAt == nil {
		t.Fatal("last_used_at not set")
	}
	missing := sha256.Sum256([]byte("nope"))
	if _, err := s.KeyByHash(ctx, missing[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key err = %v", err)
	}
}
