// Package migrations embeds the SQL migrations so the binary can apply them
// at startup.
package migrations

import "embed"

// Postgres holds goose migrations for the metadata database.
//
//go:embed postgres/*.sql
var Postgres embed.FS

// ClickHouse holds idempotent DDL for the analytics database.
//
//go:embed clickhouse/*.sql
var ClickHouse embed.FS
