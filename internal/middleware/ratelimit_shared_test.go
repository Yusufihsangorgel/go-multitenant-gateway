package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// fakeCounter is a scripted Counter. It counts like a real store and records
// the last key and window so tests can assert what the middleware asked for.
// When err is set every call fails, which is how the fail-open path is
// exercised without a real store outage.
type fakeCounter struct {
	count      int64
	err        error
	lastKey    string
	lastWindow time.Duration
}

func (f *fakeCounter) Incr(_ context.Context, key string, window time.Duration) (int64, error) {
	f.lastKey = key
	f.lastWindow = window
	if f.err != nil {
		return 0, f.err
	}
	f.count++
	return f.count, nil
}

// newSharedLimitApp mirrors newRateLimitApp: Tenant in front of the limiter,
// exactly as server.New wires it.
func newSharedLimitApp(counter Counter, limit int, window time.Duration) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Tenant())
	app.Use(RateLimitShared(counter, limit, window))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})
	return app
}

func TestRateLimitSharedAllowsUpToBudgetThenBlocks(t *testing.T) {
	const limit = 3
	fake := &fakeCounter{}
	app := newSharedLimitApp(fake, limit, time.Minute)

	for i := 1; i <= limit; i++ {
		if got := hit(t, app, "acme"); got != http.StatusOK {
			t.Fatalf("request %d/%d = %d, want %d", i, limit, got, http.StatusOK)
		}
	}
	if got := hit(t, app, "acme"); got != http.StatusTooManyRequests {
		t.Fatalf("over-budget request = %d, want %d", got, http.StatusTooManyRequests)
	}
}

func TestRateLimitSharedKeysOffTenantAndPassesWindow(t *testing.T) {
	fake := &fakeCounter{}
	window := 42 * time.Second
	app := newSharedLimitApp(fake, 10, window)

	if got := hit(t, app, "acme"); got != http.StatusOK {
		t.Fatalf("request = %d, want %d", got, http.StatusOK)
	}
	if fake.lastKey != "rl:acme" {
		t.Fatalf("counter key = %q, want %q", fake.lastKey, "rl:acme")
	}
	if fake.lastWindow != window {
		t.Fatalf("counter window = %v, want %v", fake.lastWindow, window)
	}
}

func TestRateLimitSharedFallsBackToUnknownKey(t *testing.T) {
	// No Tenant middleware in front, so nothing resolves a tenant and the
	// limiter must fall back to the shared "unknown" bucket instead of letting
	// unattributed traffic bypass limiting.
	fake := &fakeCounter{}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(RateLimitShared(fake, 10, time.Minute))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if fake.lastKey != "rl:unknown" {
		t.Fatalf("counter key = %q, want %q", fake.lastKey, "rl:unknown")
	}
}

// TestRateLimitSharedFailsOpenOnStoreError pins the deliberate tradeoff: a
// store error must let the request through, not turn a limiter outage into a
// full outage. If someone flips this to fail closed, this test is the guard.
func TestRateLimitSharedFailsOpenOnStoreError(t *testing.T) {
	fake := &fakeCounter{err: errors.New("store down")}
	app := newSharedLimitApp(fake, 1, time.Minute)

	for i := 0; i < 3; i++ {
		if got := hit(t, app, "acme"); got != http.StatusOK {
			t.Fatalf("request with failing store = %d, want %d (fail open)", got, http.StatusOK)
		}
	}
}
