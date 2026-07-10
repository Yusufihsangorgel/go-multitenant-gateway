package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// fakeQuerier scripts QueryRow responses and counts calls, so the tests can
// pin down exactly when the cache absorbs a lookup and when it goes back to
// the database.
type fakeQuerier struct {
	schema string
	err    error
	calls  int
}

type fakeRow struct {
	schema string
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.schema
	return nil
}

func (q *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	q.calls++
	return fakeRow{schema: q.schema, err: q.err}
}

func TestRegistryResolvesAndCachesHits(t *testing.T) {
	q := &fakeQuerier{schema: "tenant_acme"}
	r := NewRegistry(q, time.Minute)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		schema, found, err := r.Schema(ctx, "acme")
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if !found || schema != "tenant_acme" {
			t.Fatalf("call %d: schema=%q found=%v, want tenant_acme/true", i+1, schema, found)
		}
	}
	if q.calls != 1 {
		t.Fatalf("querier hit %d times for one tenant inside the TTL, want 1", q.calls)
	}
}

func TestRegistryCachesMisses(t *testing.T) {
	q := &fakeQuerier{err: pgx.ErrNoRows}
	r := NewRegistry(q, time.Minute)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		schema, found, err := r.Schema(ctx, "nosuch")
		if err != nil {
			t.Fatalf("call %d: a missing tenant is not an error, got %v", i+1, err)
		}
		if found || schema != "" {
			t.Fatalf("call %d: schema=%q found=%v, want empty/false", i+1, schema, found)
		}
	}
	// Misses must be cached too: junk tenant IDs arrive before the rate
	// limiter, so each one must not cost a query per request.
	if q.calls != 1 {
		t.Fatalf("querier hit %d times for one unknown tenant inside the TTL, want 1", q.calls)
	}
}

func TestRegistryExpiresEntries(t *testing.T) {
	q := &fakeQuerier{schema: "tenant_acme"}
	r := NewRegistry(q, 10*time.Millisecond)
	ctx := context.Background()

	if _, _, err := r.Schema(ctx, "acme"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, _, err := r.Schema(ctx, "acme"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if q.calls != 2 {
		t.Fatalf("querier hit %d times across an expired TTL, want 2", q.calls)
	}
}

func TestRegistryDoesNotCacheErrors(t *testing.T) {
	q := &fakeQuerier{err: errors.New("connection refused")}
	r := NewRegistry(q, time.Minute)
	ctx := context.Background()

	if _, _, err := r.Schema(ctx, "acme"); err == nil {
		t.Fatal("want the lookup error to propagate, got nil")
	}

	// The database comes back; the next call must retry instead of serving a
	// cached failure for the rest of the TTL.
	q.err = nil
	q.schema = "tenant_acme"
	schema, found, err := r.Schema(ctx, "acme")
	if err != nil {
		t.Fatalf("call after recovery: %v", err)
	}
	if !found || schema != "tenant_acme" {
		t.Fatalf("after recovery: schema=%q found=%v, want tenant_acme/true", schema, found)
	}
	if q.calls != 2 {
		t.Fatalf("querier hit %d times, want 2 (error path must not cache)", q.calls)
	}
}

func TestRegistryCacheIsBounded(t *testing.T) {
	q := &fakeQuerier{err: pgx.ErrNoRows}
	r := NewRegistry(q, time.Minute)
	ctx := context.Background()

	// A scan of unique junk IDs must not grow the cache without bound.
	for i := 0; i < registryCacheMax+10; i++ {
		if _, _, err := r.Schema(ctx, fmt.Sprintf("junk_%d", i)); err != nil {
			t.Fatalf("junk id %d: %v", i, err)
		}
	}
	r.mu.Lock()
	size := len(r.cache)
	r.mu.Unlock()
	if size > registryCacheMax {
		t.Fatalf("cache grew to %d entries, cap is %d", size, registryCacheMax)
	}
}
