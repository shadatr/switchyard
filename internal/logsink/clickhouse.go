package logsink

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/shadatr/switchyard/migrations"
)

// Open connects to ClickHouse with the native protocol.
func Open(ctx context.Context, addr, database, user, password string) (driver.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:         []string{addr},
		Auth:         clickhouse.Auth{Database: database, Username: user, Password: password},
		DialTimeout:  3 * time.Second,
		Compression:  &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		MaxOpenConns: 8,
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	return conn, nil
}

// Migrate applies the embedded, idempotent DDL files in name order.
func Migrate(ctx context.Context, conn driver.Conn) error {
	names, err := fs.Glob(migrations.ClickHouse, "clickhouse/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := fs.ReadFile(migrations.ClickHouse, n)
		if err != nil {
			return err
		}
		if err := conn.Exec(ctx, strings.TrimSpace(string(b))); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	return nil
}

// CHWriter writes batches with a prepared INSERT.
type CHWriter struct {
	Conn driver.Conn
}

// Write implements Writer.
func (w CHWriter) Write(ctx context.Context, rs []Record) error {
	b, err := w.Conn.PrepareBatch(ctx, "INSERT INTO requests")
	if err != nil {
		return err
	}
	for i := range rs {
		r := &rs[i]
		if err := b.Append(r.TS, r.RequestID, r.ProjectID, r.KeyID, r.ModelAlias, r.Target, r.Attempts, r.Status,
			r.ErrorCode, r.Stream, r.Cache, r.PromptTokens, r.CompletionTokens, r.TokensEstimated, r.CostUSD,
			r.LatencyMs, r.TTFTMs, r.UpstreamMs, r.GatewayOverheadUs); err != nil {
			_ = b.Abort()
			return err
		}
	}
	return b.Send()
}
