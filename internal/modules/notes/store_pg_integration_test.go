package notes

// Integration test against a real Postgres, gated on TEST_DATABASE_URL like
// the db and migrate package tests. It goes through the real module path:
// EnsureTenant provisions two tenants, then PGStore reads and writes with
// plain unqualified SQL and only the pinned schema keeps the rows apart.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/migrate"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
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

// cleanupTenant removes the registry row first so a concurrent MigrateAll in
// another test package cannot pick the tenant up mid-drop, then drops the
// schema.
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

func TestPGStoreIsolatesTenants(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	idA := fmt.Sprintf("notes_%d_a", suffix)
	idB := fmt.Sprintf("notes_%d_b", suffix)
	schemaA := tenant.SchemaFor(idA)
	schemaB := tenant.SchemaFor(idB)
	cleanupTenant(t, pool, idA, schemaA)
	cleanupTenant(t, pool, idB, schemaB)

	for id, schema := range map[string]string{idA: schemaA, idB: schemaB} {
		if err := migrate.EnsureTenant(ctx, pool, id, schema); err != nil {
			t.Fatalf("EnsureTenant %s: %v", id, err)
		}
	}

	store := NewPGStore(pool)

	createdA, err := store.Create(ctx, schemaA, "user-a", "from-a")
	if err != nil {
		t.Fatalf("create in %s: %v", schemaA, err)
	}
	if createdA.ID == 0 || createdA.CreatedAt.IsZero() {
		t.Fatalf("created note = %+v, want database-assigned id and timestamp", createdA)
	}
	if _, err := store.Create(ctx, schemaB, "user-b", "from-b"); err != nil {
		t.Fatalf("create in %s: %v", schemaB, err)
	}

	// The same unqualified SELECT runs for both tenants; each must see only
	// its own row.
	listA, err := store.List(ctx, schemaA)
	if err != nil {
		t.Fatalf("list %s: %v", schemaA, err)
	}
	if len(listA) != 1 || listA[0].Text != "from-a" || listA[0].UserID != "user-a" {
		t.Fatalf("tenant A notes = %+v, want exactly its own row", listA)
	}

	listB, err := store.List(ctx, schemaB)
	if err != nil {
		t.Fatalf("list %s: %v", schemaB, err)
	}
	if len(listB) != 1 || listB[0].Text != "from-b" || listB[0].UserID != "user-b" {
		t.Fatalf("tenant B notes = %+v, want exactly its own row", listB)
	}
}
