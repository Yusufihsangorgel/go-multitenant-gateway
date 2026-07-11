// Package migrate is a small hand-rolled migration runner for the
// schema-per-tenant layout. Off-the-shelf migration tools assume one schema
// per database; here the same migration set has to be applied to every tenant
// schema, and each schema keeps its own schema_migrations ledger so tenants
// migrate independently and a new tenant can be created at any point in time.
//
// Each schema migrates in a single transaction. Postgres DDL is transactional,
// so a failing migration rolls that schema back to its previous state. The
// cross-schema set is deliberately not atomic: a failure stops the run and
// leaves already-migrated schemas committed, which is the operationally sane
// outcome when one tenant's data trips a migration.
package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
)

//go:embed sql/*.sql
var embedded embed.FS

// Migration is one migration step. Version orders the steps, Name shows up in
// the ledger and in error messages, SQL runs as-is inside the per-schema
// transaction.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Load returns the migrations compiled into the binary, sorted by version.
func Load() ([]Migration, error) {
	sub, err := fs.Sub(embedded, "sql")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	return LoadFrom(sub)
}

// LoadFrom reads *.sql files at the root of fsys. Filenames follow
// NNNN_name.sql and the leading integer is the version. Sorting is numeric,
// never lexical, so version 2 always runs before version 10. Malformed names
// and duplicate versions are errors: a bad file must fail loudly at load time
// instead of silently running out of order.
func LoadFrom(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	seen := map[int]string{}
	var migs []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseFilename(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migs = append(migs, Migration{Version: version, Name: name, SQL: string(body)})
	}

	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	return migs, nil
}

// parseFilename splits NNNN_name.sql into its version and name.
func parseFilename(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	num, name, ok := strings.Cut(base, "_")
	if !ok || name == "" {
		return 0, "", fmt.Errorf("migration filename %q must look like NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(num)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("migration filename %q must start with a positive version number", filename)
	}
	return version, name, nil
}

// Apply provisions a schema and brings it up to date. Provisioning (the
// schema and its schema_migrations ledger) runs first as idempotent
// autocommit statements; the migrations then run in a single transaction
// pinned to the schema, so pending steps and their ledger rows commit or roll
// back together. The ledger lives inside the tenant schema itself, so
// dropping a tenant schema drops its migration history with it.
//
// The migration transaction opens by taking an advisory lock keyed on the
// schema name. Two runners can hit the same schema at once: replicas booting
// during a rolling deploy both run MigrateAll, or EnsureTenant races a
// MigrateAll that just read the registry. Without the lock both read an empty
// ledger, both run the DDL, and the loser dies on the ledger's primary key
// after blocking on the winner's commit. With it they serialize per schema:
// the loser waits, sees the winner's committed ledger rows, and skips them.
// The lock is transaction-scoped, so it releases itself on commit or
// rollback.
func Apply(ctx context.Context, pool *pgxpool.Pool, schema string, migs []Migration) error {
	if err := ensureSchema(ctx, pool, schema); err != nil {
		return fmt.Errorf("migrate schema %s: %w", schema, err)
	}
	err := db.WithTenantTx(ctx, pool, schema, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", schema); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}

		applied := map[int]bool{}
		rows, err := tx.Query(ctx, "SELECT version FROM schema_migrations")
		if err != nil {
			return fmt.Errorf("read applied versions: %w", err)
		}
		for rows.Next() {
			var v int64
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return fmt.Errorf("scan applied version: %w", err)
			}
			applied[int(v)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read applied versions: %w", err)
		}

		for _, m := range migs {
			if applied[m.Version] {
				continue
			}
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
				m.Version, m.Name); err != nil {
				return fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate schema %s: %w", schema, err)
	}
	return nil
}

// ensureSchema provisions a tenant schema and its ledger table outside the
// migration transaction. Both statements are idempotent and leave no partial
// state a re-run cannot finish: an empty schema with an empty ledger is
// simply a tenant at version zero.
func ensureSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	quoted, err := db.QuoteIdent(schema)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoted); err != nil && !duplicateObject(err) {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+quoted+`.schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil && !duplicateObject(err) {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// duplicateObject reports whether err is Postgres saying another session
// created the same object first. CREATE IF NOT EXISTS is not atomic against
// itself: its existence check can miss a concurrent creator's commit even
// when the statements are serialized by an advisory lock, and the loser then
// takes a unique violation on the system catalog instead of the no-op it
// expected. The object exists either way, which is all the caller asked for,
// so these errors count as success. Rolling deploys hit this for real: every
// replica provisions the same schemas at boot.
func duplicateObject(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// unique_violation (on the system catalog), duplicate_schema,
	// duplicate_table.
	return pgErr.Code == "23505" || pgErr.Code == "42P06" || pgErr.Code == "42P07"
}

// EnsureRegistry creates the public.tenants table that maps tenant IDs to
// their schemas. The registry is the single source of the schema list: every
// schema name used in a query traces back to this table, never to request
// input. Several replicas run this at boot, so a concurrent duplicate error
// is tolerated the same way ensureSchema tolerates it.
func EnsureRegistry(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.tenants (
		id text PRIMARY KEY,
		schema text NOT NULL UNIQUE,
		created_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil && !duplicateObject(err) {
		return fmt.Errorf("create tenants registry: %w", err)
	}
	return nil
}

// EnsureTenant registers a tenant, creates its schema and migrates it to the
// embedded set. Safe to call repeatedly and concurrently: registration is an
// upsert, and Apply creates the schema and skips applied versions under a
// per-schema advisory lock. Re-registering an existing ID with a different
// schema is an error, not a silent remap. The schema name is validated before
// anything is written, so a bad name never reaches the registry.
func EnsureTenant(ctx context.Context, pool *pgxpool.Pool, id, schema string) error {
	if _, err := db.QuoteIdent(schema); err != nil {
		return fmt.Errorf("tenant %s: %w", id, err)
	}
	if err := EnsureRegistry(ctx, pool); err != nil {
		return err
	}

	// No arbiter on the ON CONFLICT: the table has two unique constraints (id
	// and schema), and two replicas registering the same tenant at boot race
	// on both. Naming only (id) would let the loser die on the schema key
	// even though the row it wanted is now there. The SELECT below sorts out
	// what the no-op actually meant.
	if _, err := pool.Exec(ctx,
		"INSERT INTO public.tenants (id, schema) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		id, schema); err != nil {
		return fmt.Errorf("register tenant %s: %w", id, err)
	}
	var registered string
	err := pool.QueryRow(ctx, "SELECT schema FROM public.tenants WHERE id = $1", id).Scan(&registered)
	if errors.Is(err, pgx.ErrNoRows) {
		// The insert did nothing and no row carries this ID: the schema is
		// taken by some other tenant. Refusing is the only safe move.
		return fmt.Errorf("tenant %s: schema %s is already registered to another tenant", id, schema)
	}
	if err != nil {
		return fmt.Errorf("look up tenant %s: %w", id, err)
	}
	if registered != schema {
		return fmt.Errorf("tenant %s is already registered with schema %s, not %s", id, registered, schema)
	}

	migs, err := Load()
	if err != nil {
		return err
	}
	return Apply(ctx, pool, schema, migs)
}

// MigrateAll migrates every schema in the registry, in stable ID order. It
// stops at the first failure: the failing schema's transaction has already
// rolled back, schemas migrated before it stay committed, and the returned
// error names the tenant so the operator knows exactly where the run stopped.
func MigrateAll(ctx context.Context, pool *pgxpool.Pool) error {
	if err := EnsureRegistry(ctx, pool); err != nil {
		return err
	}
	migs, err := Load()
	if err != nil {
		return err
	}

	rows, err := pool.Query(ctx, "SELECT id, schema FROM public.tenants ORDER BY id")
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	type tenantRow struct{ id, schema string }
	var tenants []tenantRow
	for rows.Next() {
		var r tenantRow
		if err := rows.Scan(&r.id, &r.schema); err != nil {
			rows.Close()
			return fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}

	for _, t := range tenants {
		if err := Apply(ctx, pool, t.schema, migs); err != nil {
			return fmt.Errorf("tenant %s: %w", t.id, err)
		}
	}
	return nil
}
