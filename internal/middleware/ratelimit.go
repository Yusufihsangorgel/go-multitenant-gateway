package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// RateLimit applies a fixed-window limit per tenant.
//
// This reference keeps the counters in memory, which is fine for a single
// instance. In production the gateway runs several replicas, so an in-memory
// counter would let each replica grant the full budget; there the counter
// lives in Redis so all replicas share it. The window is intentionally simple
// (fixed window, not a sliding log) — cheap on the hot path, and good enough
// unless you need burst-exact fairness.
func RateLimit(perMinute int) fiber.Handler {
	var mu sync.Mutex
	type window struct {
		count int
		reset time.Time
	}
	windows := map[string]*window{}

	return func(c *fiber.Ctx) error {
		key := "unknown"
		if t, ok := tenant.From(c); ok {
			key = t.ID
		}

		mu.Lock()
		w, ok := windows[key]
		now := time.Now()
		if !ok || now.After(w.reset) {
			w = &window{reset: now.Add(time.Minute)}
			windows[key] = w
		}
		w.count++
		over := w.count > perMinute
		mu.Unlock()

		if over {
			return fiber.NewError(fiber.StatusTooManyRequests, "rate limit exceeded")
		}
		return c.Next()
	}
}
