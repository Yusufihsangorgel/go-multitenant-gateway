// Package server wires the middleware chain and modules into a Fiber app.
// It is separate from main so tests can build the same app the binary runs.
package server

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/config"
	mw "github.com/Yusufihsangorgel/go-multitenant-gateway/internal/middleware"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules/health"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules/notes"
)

// New builds the gateway app. Health is mounted before the tenant/auth chain so
// probes need no token; every other module is scoped to a tenant and requires a
// valid bearer token.
func New(cfg config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "go-multitenant-gateway",
		DisableStartupMessage: true,
	})

	app.Use(recover.New())
	modules.Mount(app, health.New())

	app.Use(mw.Tenant())
	app.Use(mw.RateLimit(cfg.RatePerMinute))
	app.Use(mw.Auth(cfg.JWTSecret))

	modules.Mount(app, notes.New())

	return app
}
