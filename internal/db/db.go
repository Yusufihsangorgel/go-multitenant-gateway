// Package db owns the Postgres connection pool and the one primitive every
// tenant-scoped query goes through: a transaction whose search_path is pinned
// to a single tenant schema.
//
// The pinning uses SET LOCAL inside an explicit transaction. SET LOCAL scopes
// the setting to that transaction only, so when the pooled connection is
// released and reused for another tenant, no search_path travels with it. A
// session-level SET would stick to the connection and leak across requests,
// which in a multi-tenant gateway means reading another tenant's rows. The
// path is pinned to the tenant schema alone, with no public fallback: a query
// that accidentally targets a table that only exists in public fails loudly
// instead of quietly reading shared data.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pgx pool for the given URL and verifies it with a ping so a
// bad DSN fails at boot, not on the first request. maxConns caps the pool when
// positive; zero or negative keeps the pgx default.
func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// WithTenantTx runs fn inside a transaction whose search_path is pinned to
// exactly one tenant schema. Queries inside fn use plain unqualified table
// names and resolve in that schema. The schema name goes through QuoteIdent
// because identifiers cannot be bound as parameters. The SET LOCAL must run
// inside the transaction (outside one it is a silent no-op), which is why the
// pin happens strictly between Begin and fn. Commit on success, rollback on
// any error.
func WithTenantTx(ctx context.Context, pool *pgxpool.Pool, schema string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	quoted, err := QuoteIdent(schema)
	if err != nil {
		return fmt.Errorf("tenant schema: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tenant tx: %w", err)
	}
	// Rollback after a successful commit is a harmless no-op; this defer is
	// what guarantees the tx never outlives an error path.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+quoted); err != nil {
		return fmt.Errorf("pin search_path to %s: %w", schema, err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tenant tx: %w", err)
	}
	return nil
}
