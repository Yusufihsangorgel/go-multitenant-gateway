package config

import (
	"os"
	"strconv"
)

// Config holds runtime settings. Everything comes from the environment so the
// same binary runs in dev and prod without code changes.
type Config struct {
	Port          string
	JWTSecret     []byte
	RatePerMinute int
}

// Load reads config from the environment, falling back to sane dev defaults.
func Load() Config {
	return Config{
		Port:          env("PORT", "8080"),
		JWTSecret:     []byte(env("JWT_SECRET", "dev-secret-change-me")),
		RatePerMinute: envInt("RATE_PER_MINUTE", 120),
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
