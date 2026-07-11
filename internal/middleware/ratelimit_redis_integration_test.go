package middleware

// Integration test against a real Redis, gated on TEST_REDIS_URL (CI sets it;
// locally `docker compose up -d` and export TEST_REDIS_URL=redis://localhost:6379/0).
// Tenant IDs get a nanosecond suffix so reruns against a long-lived Redis never
// see a stale counter.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisCounterSharesBudgetAcrossReplicas builds two independent fiber apps
// over one RedisCounter, simulating two gateway replicas. The budget must be
// spent jointly (requests on either app count against the same window) and the
// key's TTL must reset the window once it elapses.
func TestRedisCounterSharesBudgetAcrossReplicas(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	const limit = 3
	window := 2 * time.Second
	counter := NewRedisCounter(rdb)

	newReplica := func() *fiber.App {
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		app.Use(Tenant())
		app.Use(RateLimitShared(counter, limit, window))
		app.Get("/", func(c *fiber.Ctx) error {
			return c.SendStatus(http.StatusOK)
		})
		return app
	}
	replicaA := newReplica()
	replicaB := newReplica()

	tenantID := fmt.Sprintf("redis_it_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = rdb.Del(context.Background(), "rl:"+tenantID).Err()
	})

	// app.Test defaults to a 1s timeout; -1 disables it so a slow Redis round
	// trip cannot flake the test.
	hitReplica := func(app *fiber.App) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Tenant", tenantID)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		return resp.StatusCode
	}

	// Two requests on replica A and one on replica B spend the whole budget of
	// 3, even though no single replica saw more than two requests.
	for i := 1; i <= 2; i++ {
		if got := hitReplica(replicaA); got != http.StatusOK {
			t.Fatalf("replica A request %d = %d, want %d", i, got, http.StatusOK)
		}
	}
	if got := hitReplica(replicaB); got != http.StatusOK {
		t.Fatalf("replica B request = %d, want %d", got, http.StatusOK)
	}

	// The budget is shared, so the fourth request is over, on either replica.
	if got := hitReplica(replicaB); got != http.StatusTooManyRequests {
		t.Fatalf("4th request on B = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := hitReplica(replicaA); got != http.StatusTooManyRequests {
		t.Fatalf("5th request on A = %d, want %d", got, http.StatusTooManyRequests)
	}

	// Wait out the window (TTL started at the first request) and the budget
	// must be fresh again.
	time.Sleep(window + 500*time.Millisecond)
	if got := hitReplica(replicaA); got != http.StatusOK {
		t.Fatalf("request after window reset = %d, want %d", got, http.StatusOK)
	}
}
