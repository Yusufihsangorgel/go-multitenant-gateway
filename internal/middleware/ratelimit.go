package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// maxRateWindows bounds the in-memory window map. The key is a tenant ID, which
// in the DB-off quickstart is an unvalidated header, so a stream of unique
// values must not grow the map forever. Past the cap expired windows are
// dropped, and if a flood fills the map faster than windows expire the rest are
// cleared too.
const maxRateWindows = 10000

// rateWindow is a fixed-window counter: count requests until reset passes.
type rateWindow struct {
	count int
	reset time.Time
}

// rateLimiter holds the per-tenant fixed-window counters behind a mutex. It is
// a value the handler closes over; kept as a named type so the map bound is
// exercised by tests.
type rateLimiter struct {
	perMinute int

	mu      sync.Mutex
	windows map[string]*rateWindow
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{perMinute: perMinute, windows: map[string]*rateWindow{}}
}

// allow records one request for key and reports whether it is within budget.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.windows[key]
	if !ok || now.After(w.reset) {
		if len(l.windows) >= maxRateWindows {
			l.evict(now)
		}
		w = &rateWindow{reset: now.Add(time.Minute)}
		l.windows[key] = w
	}
	w.count++
	return w.count <= l.perMinute
}

// evict keeps the window map bounded. Expired windows are dropped first; if a
// flood of unique keys fills the map faster than windows expire, the rest are
// cleared too. Clearing only ever hands an active tenant a fresh window, which
// grants budget rather than denying it, so the fixed-window guarantee holds.
func (l *rateLimiter) evict(now time.Time) {
	for k, w := range l.windows {
		if now.After(w.reset) {
			delete(l.windows, k)
		}
	}
	if len(l.windows) >= maxRateWindows {
		l.windows = map[string]*rateWindow{}
	}
}

func (l *rateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.windows)
}

// RateLimit applies a fixed-window limit per tenant.
//
// This reference keeps the counters in memory, which is fine for a single
// instance. In production the gateway runs several replicas, so an in-memory
// counter would let each replica grant the full budget; there the counter
// lives in Redis so all replicas share it. The window is intentionally simple
// (fixed window, not a sliding log) — cheap on the hot path, and good enough
// unless you need burst-exact fairness.
func RateLimit(perMinute int) fiber.Handler {
	l := newRateLimiter(perMinute)

	return func(c *fiber.Ctx) error {
		key := "unknown"
		if t, ok := tenant.From(c); ok {
			key = t.ID
		}
		if !l.allow(key) {
			return fiber.NewError(fiber.StatusTooManyRequests, "rate limit exceeded")
		}
		return c.Next()
	}
}
