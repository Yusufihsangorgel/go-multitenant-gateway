package middleware

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// Counter is the shared store behind RateLimitShared. Incr bumps the counter
// for key and returns the count after the increment; the implementation is
// responsible for expiring the key at the window boundary, which is what makes
// the window fixed.
type Counter interface {
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
}

// RateLimitShared applies the same fixed-window limit as RateLimit, but keeps
// the counter in a shared store so every replica spends from one budget per
// tenant. This is the production variant RateLimit's comment points at: with
// several replicas an in-process counter would grant each replica the full
// budget.
//
// On a store error the limiter fails open: the request passes and the error is
// logged. A limiter outage throttling all traffic to zero is worse than a
// window of unlimited requests. Availability over strict limiting; see
// ARCHITECTURE.md.
func RateLimitShared(counter Counter, limit int, window time.Duration) fiber.Handler {
	var lastLogged atomic.Int64
	return func(c *fiber.Ctx) error {
		key := "rl:unknown"
		if t, ok := tenant.From(c); ok {
			key = "rl:" + t.ID
		}

		count, err := counter.Incr(c.UserContext(), key, window)
		if err != nil {
			// Fail open, but log at most once per second per process. During
			// a store outage every request takes this path, and one identical
			// line per request would flood the log at exactly the wrong time.
			now := time.Now().Unix()
			if last := lastLogged.Load(); now != last && lastLogged.CompareAndSwap(last, now) {
				log.Printf("rate limit store error, failing open: %v", err)
			}
			return c.Next()
		}
		if count > int64(limit) {
			return fiber.NewError(fiber.StatusTooManyRequests, "rate limit exceeded")
		}
		return c.Next()
	}
}
