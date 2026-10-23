// Package store is the Postgres repository for projects and API keys.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/shadatr/switchyard/migrations"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Key is an API key record. The plaintext key is never stored.
type Key struct {
	ID               string
	ProjectID        string
	ProjectName      string
	Name             string
	Prefix           string
	AllowedModels    []string
	RPMLimit         *int
	TPMLimit         *int
	MonthlyBudgetUSD *float64
	Disabled         bool
	CreatedAt        time.Time
	LastUsedAt       *time.Time
}

// AllowsModel reports whether alias may be used with this key.
func (k *Key) AllowsModel(alias string) bool {
	if len(k.AllowedModels) == 0 {
		return true
	}
	for _, m := range k.AllowedModels {
		if m == alias {
			return true
		}
	}
	return false
}

// NewKey is the input for CreateKey.
type NewKey struct {
	ProjectName      string
	Name             string
	Prefix           string
	Hash             []byte
	AllowedModels    []string
	RPMLimit         *int
	TPMLimit         *int
	MonthlyBudgetUSD *float64
}

// KeyUpdate holds optional changes; nil fields are left unchanged.
type KeyUpdate struct {
	Name             *string
	AllowedModels    *[]string
	RPMLimit         **int
	TPMLimit         **int
	MonthlyBudgetUSD **float64
	Disabled         *bool
}

// Store wraps a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies embedded goose migrations.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	goose.SetBaseFS(migrations.Postgres)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "postgres")
}

const keyColumns = `k.id, k.project_id, p.name, k.name, k.prefix, k.allowed_models, k.rpm_limit, k.tpm_limit,
	k.monthly_budget_usd::float8, k.disabled, k.created_at, k.last_used_at`

func scanKey(row pgx.Row) (*Key, error) {
	var k Key
	var id, pid pgtype.UUID
	var rpm, tpm sql.NullInt32
	var budget sql.NullFloat64
	if err := row.Scan(&id, &pid, &k.ProjectName, &k.Name, &k.Prefix, &k.AllowedModels, &rpm, &tpm,
		&budget, &k.Disabled, &k.CreatedAt, &k.LastUsedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	k.ID, k.ProjectID = id.String(), pid.String()
	if rpm.Valid {
		v := int(rpm.Int32)
		k.RPMLimit = &v
	}
	if tpm.Valid {
		v := int(tpm.Int32)
		k.TPMLimit = &v
	}
	if budget.Valid {
		v := budget.Float64
		k.MonthlyBudgetUSD = &v
	}
	return &k, nil
}

// KeyByHash looks up a key by the SHA-256 of its plaintext.
func (s *Store) KeyByHash(ctx context.Context, hash []byte) (*Key, error) {
	return scanKey(s.pool.QueryRow(ctx,
		`SELECT `+keyColumns+` FROM api_keys k JOIN projects p ON p.id = k.project_id WHERE k.hash = $1`, hash))
}

// KeyByID returns one key.
func (s *Store) KeyByID(ctx context.Context, id string) (*Key, error) {
	return scanKey(s.pool.QueryRow(ctx,
		`SELECT `+keyColumns+` FROM api_keys k JOIN projects p ON p.id = k.project_id WHERE k.id = $1`, id))
}

// ListKeys returns all keys, newest first.
func (s *Store) ListKeys(ctx context.Context) ([]*Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+keyColumns+` FROM api_keys k JOIN projects p ON p.id = k.project_id ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CreateKey inserts a key, creating the project if needed.
func (s *Store) CreateKey(ctx context.Context, nk NewKey) (*Key, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var projectID pgtype.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`, nk.ProjectName).Scan(&projectID); err != nil {
		return nil, fmt.Errorf("upsert project: %w", err)
	}
	if nk.AllowedModels == nil {
		nk.AllowedModels = []string{}
	}
	var id pgtype.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO api_keys (project_id, name, prefix, hash, allowed_models, rpm_limit, tpm_limit, monthly_budget_usd)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		projectID, nk.Name, nk.Prefix, nk.Hash, nk.AllowedModels, nk.RPMLimit, nk.TPMLimit, nk.MonthlyBudgetUSD,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("insert key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.KeyByID(ctx, id.String())
}

// UpdateKey applies a partial update.
func (s *Store) UpdateKey(ctx context.Context, id string, u KeyUpdate) (*Key, error) {
	sets := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if u.Name != nil {
		add("name", *u.Name)
	}
	if u.AllowedModels != nil {
		m := *u.AllowedModels
		if m == nil {
			m = []string{}
		}
		add("allowed_models", m)
	}
	if u.RPMLimit != nil {
		add("rpm_limit", *u.RPMLimit)
	}
	if u.TPMLimit != nil {
		add("tpm_limit", *u.TPMLimit)
	}
	if u.MonthlyBudgetUSD != nil {
		add("monthly_budget_usd", *u.MonthlyBudgetUSD)
	}
	if u.Disabled != nil {
		add("disabled", *u.Disabled)
	}
	if len(sets) > 0 {
		q := "UPDATE api_keys SET " + strings.Join(sets, ", ") + " WHERE id = $1"
		tag, err := s.pool.Exec(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	return s.KeyByID(ctx, id)
}

// TouchKeys sets last_used_at = now() for the given key IDs.
func (s *Store) TouchKeys(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = ANY($1::uuid[])`, ids)
	return err
}
