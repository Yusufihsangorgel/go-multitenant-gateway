package migrate

// Integration tests against a real Postgres, gated on TEST_DATABASE_URL just
// like the db package tests. Tenant IDs and schema names get a nanosecond
// suffix so reruns against a long-lived database never see stale ledgers.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.Connect(context.Background(), url, 4)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupTenant removes the registry row and drops the schema so the shared
// database stays clean across runs. The registry row goes first so a
// concurrent MigrateAll cannot pick the tenant up mid-drop.
func cleanupTenant(t *testing.T, pool *pgxpool.Pool, id, schema string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM public.tenants WHERE id = $1", id)
		if quoted, err := db.QuoteIdent(schema); err == nil {
			_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		}
	})
}

func tableExists(t *testing.T, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)",
		schema, table).Scan(&exists)
	if err != nil {
		t.Fatalf("check %s.%s: %v", schema, table, err)
	}
	return exists
}

func ledgerCount(t *testing.T, pool *pgxpool.Pool, schema string) int {
	t.Helper()
	quoted, err := db.QuoteIdent(schema)
	if err != nil {
		t.Fatalf("quote %q: %v", schema, err)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+quoted+".schema_migrations").Scan(&n); err != nil {
		t.Fatalf("count ledger rows in %s: %v", schema, err)
	}
	return n
}

func TestEnsureTenantAndMigrateAllAreIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	id := fmt.Sprintf("idem_%d", time.Now().UnixNano())
	schema := "tenant_" + id
	cleanupTenant(t, pool, id, schema)

	// Running the whole pipeline twice must change nothing the second time.
	for i := 0; i < 2; i++ {
		if err := EnsureTenant(ctx, pool, id, schema); err != nil {
			t.Fatalf("EnsureTenant run %d: %v", i+1, err)
		}
		if err := MigrateAll(ctx, pool); err != nil {
			t.Fatalf("MigrateAll run %d: %v", i+1, err)
		}
	}

	if !tableExists(t, pool, schema, "notes") {
		t.Fatal("notes table missing after migration")
	}
	migs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := ledgerCount(t, pool, schema); got != len(migs) {
		t.Fatalf("ledger has %d rows after repeated runs, want %d", got, len(migs))
	}
}

// TestConcurrentEnsureTenantsDoNotRace covers the rolling-deploy case: several
// replicas boot at once and every one of them registers and migrates the same
// tenant. The per-schema advisory lock has to serialize the schema creation,
// the ledger creation and the DDL; without it the losers die on catalog or
// primary key conflicts and a boot crash-loops on timing luck.
func TestConcurrentEnsureTenantsDoNotRace(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	id := fmt.Sprintf("conc_%d", time.Now().UnixNano())
	schema := "tenant_" + id
	cleanupTenant(t, pool, id, schema)

	const runners = 8
	errs := make(chan error, runners)
	for i := 0; i < runners; i++ {
		go func() {
			errs <- EnsureTenant(ctx, pool, id, schema)
		}()
	}
	for i := 0; i < runners; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent EnsureTenant: %v", err)
		}
	}

	if !tableExists(t, pool, schema, "notes") {
		t.Fatal("notes table missing after concurrent migration")
	}
	migs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := ledgerCount(t, pool, schema); got != len(migs) {
		t.Fatalf("ledger has %d rows after %d concurrent runners, want %d", got, runners, len(migs))
	}
}

func TestEnsureTenantRejectsSchemaRemap(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	id := fmt.Sprintf("remap_%d", time.Now().UnixNano())
	schema := "tenant_" + id
	cleanupTenant(t, pool, id, schema)

	if err := EnsureTenant(ctx, pool, id, schema); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}
	// Same ID, different schema: must error instead of silently remapping.
	if err := EnsureTenant(ctx, pool, id, schema+"_other"); err == nil {
		t.Fatal("want schema mismatch error, got nil")
	}
}

// TestApplyHalfwayFailureKeepsCommittedSchemas exercises the deliberate
// non-atomicity across schemas: schema A migrates and commits, schema B hits a
// broken migration and rolls back completely, and A is untouched. Re-running B
// with the good set then recovers it, which is the whole point of the
// per-schema transaction plus ledger design.
func TestApplyHalfwayFailureKeepsCommittedSchemas(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	idA := fmt.Sprintf("half_%d_a", suffix)
	idB := fmt.Sprintf("half_%d_b", suffix)
	schemaA := "tenant_" + idA
	schemaB := "tenant_" + idB
	cleanupTenant(t, pool, idA, schemaA)
	cleanupTenant(t, pool, idB, schemaB)

	good, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Schema A gets the good set and commits. Apply provisions the schema and
	// ledger itself; no manual setup.
	if err := Apply(ctx, pool, schemaA, good); err != nil {
		t.Fatalf("Apply to %s: %v", schemaA, err)
	}

	// Schema B gets the good set plus a broken migration. The whole
	// transaction must roll back, taking the good migrations and the ledger
	// with it.
	broken := append(append([]Migration{}, good...), Migration{
		Version: 9999,
		Name:    "broken",
		SQL:     "THIS IS NOT SQL",
	})
	if err := Apply(ctx, pool, schemaB, broken); err == nil {
		t.Fatal("want broken migration to fail, got nil")
	}

	if !tableExists(t, pool, schemaA, "notes") {
		t.Fatal("schema A lost its notes table after B failed")
	}
	if got := ledgerCount(t, pool, schemaA); got != len(good) {
		t.Fatalf("schema A ledger has %d rows, want %d", got, len(good))
	}
	if tableExists(t, pool, schemaB, "notes") {
		t.Fatal("schema B has a notes table from a rolled-back transaction")
	}
	// Provisioning is deliberately outside the migration transaction, so the
	// empty schema and its empty ledger survive the rollback: schema B is a
	// tenant at version zero, not a half-migrated one.
	if !tableExists(t, pool, schemaB, "schema_migrations") {
		t.Fatal("schema B lost its ledger table; provisioning must survive a failed migration")
	}
	if got := ledgerCount(t, pool, schemaB); got != 0 {
		t.Fatalf("schema B ledger has %d rows after a rolled-back migration, want 0", got)
	}

	// Recovery: the good set applies cleanly to B after the failure.
	if err := Apply(ctx, pool, schemaB, good); err != nil {
		t.Fatalf("recovery Apply to %s: %v", schemaB, err)
	}
	if !tableExists(t, pool, schemaB, "notes") {
		t.Fatal("schema B missing notes table after recovery")
	}
	if got := ledgerCount(t, pool, schemaB); got != len(good) {
		t.Fatalf("schema B ledger has %d rows after recovery, want %d", got, len(good))
	}
}
