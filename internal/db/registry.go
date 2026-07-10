package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier is the single query method Registry needs. *pgxpool.Pool satisfies
// it, and unit tests substitute a fake so the cache behavior is testable
// without a database.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// registryCacheMax bounds the cache. Past the cap the cache resets instead of
// evicting, which is crude but keeps a scan of unique junk tenant IDs from
// growing the map forever; a reset costs one extra query per active tenant.
const registryCacheMax = 10000

// Registry resolves tenant IDs to schema names from the public.tenants table,
// which the migrate package populates. This is the serving-path side of the
// registry: the tenant middleware asks it for every request's schema, so a
// schema name that reaches SQL always traces back to a registered row and
// never to the raw header value.
//
// Lookups are cached for a short TTL, hits and misses both. The tenant
// middleware runs before the rate limiter, so without the cache a flood of
// requests carrying junk tenant headers would turn into one database query
// each. The TTL also bounds staleness: a tenant registered mid-flight becomes
// visible within one TTL, and a removed one stops resolving just as fast.
type Registry struct {
	q   Querier
	ttl time.Duration

	mu    sync.Mutex
	cache map[string]registryEntry
}

type registryEntry struct {
	schema  string
	found   bool
	expires time.Time
}

// NewRegistry wraps an already-connected Querier. The caller owns its
// lifecycle; the server closes the pool on shutdown.
func NewRegistry(q Querier, ttl time.Duration) *Registry {
	return &Registry{q: q, ttl: ttl, cache: map[string]registryEntry{}}
}

// Schema returns the schema registered for the tenant ID. found is false when
// the tenant is not in the registry. Errors are never cached: one failed
// query must not pin a tenant unresolvable for a whole TTL.
func (r *Registry) Schema(ctx context.Context, id string) (schema string, found bool, err error) {
	now := time.Now()
	r.mu.Lock()
	if e, ok := r.cache[id]; ok && now.Before(e.expires) {
		r.mu.Unlock()
		return e.schema, e.found, nil
	}
	r.mu.Unlock()

	found = true
	err = r.q.QueryRow(ctx, "SELECT schema FROM public.tenants WHERE id = $1", id).Scan(&schema)
	if errors.Is(err, pgx.ErrNoRows) {
		schema, found, err = "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("look up tenant %s: %w", id, err)
	}

	r.mu.Lock()
	if len(r.cache) >= registryCacheMax {
		r.cache = map[string]registryEntry{}
	}
	r.cache[id] = registryEntry{schema: schema, found: found, expires: now.Add(r.ttl)}
	r.mu.Unlock()
	return schema, found, nil
}
