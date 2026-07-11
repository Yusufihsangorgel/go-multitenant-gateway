package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds runtime settings. Everything comes from the environment so the
// same binary runs in dev and prod without code changes.
type Config struct {
	Port          string
	JWTSecret     []byte
	RatePerMinute int

	// DatabaseURL is the Postgres DSN. Empty means the notes module runs on
	// its in-memory store and the binary needs no database at all.
	DatabaseURL string
	// RedisURL selects the shared rate limiter. Empty keeps the in-memory
	// limiter, which is correct for a single instance.
	RedisURL string
	// DBMaxConns caps the pgx pool. Zero or negative keeps the pgx default.
	DBMaxConns int32
	// SeedTenants lists tenant IDs to register and migrate at boot when
	// DatabaseURL is set. Tenants can also be added later through the
	// registry; this just makes a fresh database usable immediately.
	SeedTenants []string
}

// Load reads config from the environment, falling back to sane dev defaults.
func Load() Config {
	return Config{
		Port:          env("PORT", "8080"),
		JWTSecret:     []byte(env("JWT_SECRET", "dev-secret-change-me")),
		RatePerMinute: envInt("RATE_PER_MINUTE", 120),
		DatabaseURL:   env("DATABASE_URL", ""),
		RedisURL:      env("REDIS_URL", ""),
		DBMaxConns:    int32(envInt("DB_MAX_CONNS", 8)),
		SeedTenants:   splitList(env("SEED_TENANTS", "")),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// splitList turns a comma-separated env value into a clean slice, dropping
// empty entries so "a,,b" and trailing commas do not produce ghost tenants.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
