package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// newRateLimitApp wires Tenant in front of RateLimit, exactly as server.New
// does, so the limiter keys off the X-Tenant header the way it does in
// production. The handler just returns 200 when the request is allowed through.
func newRateLimitApp(perMinute int) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Tenant())
	app.Use(RateLimit(perMinute))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})
	return app
}

// hit sends one request for the given tenant and returns the status code. All
// calls in a test run within milliseconds, so they land in the same fixed
// window — the limit is reached by request count, never by wall-clock timing,
// which keeps these tests deterministic without any sleeps.
func hit(t *testing.T, app *fiber.App, tenantID string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", tenantID)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp.StatusCode
}

func TestRateLimitAllowsUpToBudgetThenBlocks(t *testing.T) {
	const limit = 3
	app := newRateLimitApp(limit)

	// The first `limit` requests are within budget.
	for i := 1; i <= limit; i++ {
		if got := hit(t, app, "acme"); got != http.StatusOK {
			t.Fatalf("request %d/%d = %d, want %d", i, limit, got, http.StatusOK)
		}
	}
	// The next one in the same window is over budget.
	if got := hit(t, app, "acme"); got != http.StatusTooManyRequests {
		t.Fatalf("over-budget request = %d, want %d", got, http.StatusTooManyRequests)
	}
}

func TestRateLimitIsPerTenant(t *testing.T) {
	const limit = 2
	app := newRateLimitApp(limit)

	// Exhaust tenant A's budget and confirm the next request is blocked.
	for i := 1; i <= limit; i++ {
		if got := hit(t, app, "tenant-a"); got != http.StatusOK {
			t.Fatalf("tenant-a request %d = %d, want %d", i, got, http.StatusOK)
		}
	}
	if got := hit(t, app, "tenant-a"); got != http.StatusTooManyRequests {
		t.Fatalf("tenant-a over budget = %d, want %d", got, http.StatusTooManyRequests)
	}

	// Tenant B keeps its own window, so A hitting the ceiling must not affect it.
	if got := hit(t, app, "tenant-b"); got != http.StatusOK {
		t.Fatalf("tenant-b first request = %d, want %d", got, http.StatusOK)
	}
}
