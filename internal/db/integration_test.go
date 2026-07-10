package db

// Integration tests against a real Postgres. They run only when
// TEST_DATABASE_URL is set (CI sets it; locally `docker compose up -d` and
// export TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/gateway?sslmode=disable).
// Schema names get a nanosecond suffix so reruns against a long-lived database
// never trip over stale state.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool skips the test unless TEST_DATABASE_URL is set, then connects with
// a small pool so connections get reused aggressively, which is exactly the
// condition under which a search_path leak would show up.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := Connect(context.Background(), url, 4)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// makeSchema creates a throwaway schema and registers a cleanup that drops it.
func makeSchema(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	quoted, err := QuoteIdent(name)
	if err != nil {
		t.Fatalf("quote schema %q: %v", name, err)
	}
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
	})
}

// normalizeSearchPath strips quoting and spaces so the comparison does not
// depend on how Postgres chooses to render the GUC value.
func normalizeSearchPath(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func TestWithTenantTxIsolatesSchemas(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	schemaA := fmt.Sprintf("gwtest_%d_a", suffix)
	schemaB := fmt.Sprintf("gwtest_%d_b", suffix)
	makeSchema(t, pool, schemaA)
	makeSchema(t, pool, schemaB)

	// The exact same unqualified statements run against both schemas. Only the
	// pinned search_path decides where the table and its rows live.
	for schema, value := range map[string]string{schemaA: "from-a", schemaB: "from-b"} {
		err := WithTenantTx(ctx, pool, schema, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "CREATE TABLE items (v text)"); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO items (v) VALUES ($1)", value)
			return err
		})
		if err != nil {
			t.Fatalf("seed schema %s: %v", schema, err)
		}
	}

	for schema, want := range map[string]string{schemaA: "from-a", schemaB: "from-b"} {
		var got string
		var count int
		err := WithTenantTx(ctx, pool, schema, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&count); err != nil {
				return err
			}
			return tx.QueryRow(ctx, "SELECT v FROM items").Scan(&got)
		})
		if err != nil {
			t.Fatalf("read schema %s: %v", schema, err)
		}
		if count != 1 || got != want {
			t.Fatalf("schema %s: got %d rows, v=%q, want 1 row, v=%q", schema, count, got, want)
		}
	}
}

func TestWithTenantTxRollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	schema := fmt.Sprintf("gwtest_%d_rb", time.Now().UnixNano())
	makeSchema(t, pool, schema)

	err := WithTenantTx(ctx, pool, schema, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "CREATE TABLE items (v text)"); err != nil {
			return err
		}
		return fmt.Errorf("forced failure")
	})
	if err == nil {
		t.Fatal("want the fn error to propagate, got nil")
	}

	// The whole transaction must have rolled back, so the table cannot exist.
	var exists bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'items')",
		schema).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if exists {
		t.Fatal("items table survived a rolled-back transaction")
	}
}

// TestWithTenantTxDoesNotLeakSearchPath hammers the pool from many goroutines
// across two schemas. Inside every transaction the search_path must be the
// pinned schema and nothing else; after every transaction a raw pool query
// must see the connection's default path again. If SET LOCAL were ever
// replaced with a session-level SET, connection reuse would make this fail.
// Run with -race, as CI does.
func TestWithTenantTxDoesNotLeakSearchPath(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	schemas := []string{
		fmt.Sprintf("gwtest_%d_h1", suffix),
		fmt.Sprintf("gwtest_%d_h2", suffix),
	}
	for _, s := range schemas {
		makeSchema(t, pool, s)
	}

	const goroutines = 32
	const iterations = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				schema := schemas[(g+i)%len(schemas)]

				var inside string
				err := WithTenantTx(ctx, pool, schema, func(ctx context.Context, tx pgx.Tx) error {
					return tx.QueryRow(ctx, "SELECT current_setting('search_path')").Scan(&inside)
				})
				if err != nil {
					t.Errorf("goroutine %d iter %d: %v", g, i, err)
					return
				}
				if normalizeSearchPath(inside) != schema {
					t.Errorf("inside tx: search_path = %q, want pinned %q", inside, schema)
					return
				}

				// A plain pool query runs on some released connection. Whatever
				// its default path is, it must not be a single pinned schema.
				var outside string
				if err := pool.QueryRow(ctx, "SELECT current_setting('search_path')").Scan(&outside); err != nil {
					t.Errorf("goroutine %d iter %d raw query: %v", g, i, err)
					return
				}
				got := normalizeSearchPath(outside)
				for _, s := range schemas {
					if got == s {
						t.Errorf("after tx: search_path %q leaked out of the transaction", outside)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
