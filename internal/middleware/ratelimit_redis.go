package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCounter implements Counter on Redis. INCR and EXPIRE NX are pipelined
// into one round trip: INCR creates the key at 1 or bumps it, EXPIRE NX sets
// the window TTL only when the key has none, so the first request in a window
// starts the clock and later requests leave it alone. EXPIRE NX needs Redis 7
// or newer, which is what docker-compose.yml and CI pin.
type RedisCounter struct {
	rdb *redis.Client
}

// NewRedisCounter wraps an already-connected client. The caller owns the
// client's lifecycle; the server closes it on shutdown.
func NewRedisCounter(rdb *redis.Client) *RedisCounter {
	return &RedisCounter{rdb: rdb}
}

// Incr bumps the counter for key and returns the count after the increment.
func (rc *RedisCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	pipe := rc.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("increment rate limit key %s: %w", key, err)
	}
	return incr.Val(), nil
}
