// Package server wires the middleware chain and modules into a Fiber app.
// It is separate from main so tests can build the same app the binary runs.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/config"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
	mw "github.com/Yusufihsangorgel/go-multitenant-gateway/internal/middleware"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/migrate"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules/health"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/modules/notes"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// registrySchemaTTL is how long a tenant-to-schema resolution is cached on
// the serving path. Short enough that a tenant registered at runtime becomes
// routable within seconds, long enough that request traffic does not turn
// into registry queries.
const registrySchemaTTL = 30 * time.Second

// errorHandler keeps the deliberate errors (*fiber.Error carries a status and
// a message written for the client) and hides everything else. A raw error at
// this point is a backend failure whose text can carry schema names, SQL and
// connection detail; that belongs in the log, not in a response body.
func errorHandler(c *fiber.Ctx, err error) error {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).SendString(fe.Message)
	}
	log.Printf("%s %s: %v", c.Method(), c.Path(), err)
	return c.Status(fiber.StatusInternalServerError).SendString("internal error")
}

// App bundles the Fiber app with the clients it owns, so shutdown can drain
// requests first and close the backends after.
type App struct {
	Fiber *fiber.App

	pool *pgxpool.Pool
	rdb  *redis.Client
}

// New builds the gateway app. Health is mounted before the tenant/auth chain
// so probes need no token; every other module is scoped to a tenant and
// requires a valid bearer token.
//
// The backends are selected by env. With DATABASE_URL set the notes module
// runs on Postgres, tenant resolution goes through the registry (a request
// for an unregistered tenant is rejected before any SQL runs), and boot
// registers the seed tenants and migrates every schema in the registry, so
// the process never serves requests against a half-migrated database. With
// REDIS_URL set the rate limiter counts in Redis so all replicas share one
// budget. With both empty nothing touches the network and the app behaves
// exactly like the zero-dependency quickstart.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	a := &App{}

	notesModule := notes.New()
	tenantMW := mw.Tenant()
	if cfg.DatabaseURL != "" {
		pool, err := db.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
		if err != nil {
			return nil, err
		}
		a.pool = pool

		if err := migrate.EnsureRegistry(ctx, pool); err != nil {
			a.closeClients()
			return nil, err
		}
		for _, id := range cfg.SeedTenants {
			if err := migrate.EnsureTenant(ctx, pool, id, tenant.SchemaFor(id)); err != nil {
				a.closeClients()
				return nil, fmt.Errorf("seed tenant %s: %w", id, err)
			}
		}
		if err := migrate.MigrateAll(ctx, pool); err != nil {
			a.closeClients()
			return nil, err
		}
		notesModule = notes.NewWithStore(notes.NewPGStore(pool))
		// With a database present the schema for each request comes from the
		// tenants registry, not from the naming convention, so the only thing
		// a header can select is a tenant that was deliberately registered.
		tenantMW = mw.TenantFrom(db.NewRegistry(pool, registrySchemaTTL).Schema)
	}

	limiter := mw.RateLimit(cfg.RatePerMinute)
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			a.closeClients()
			return nil, fmt.Errorf("parse redis url: %w", err)
		}
		rdb := redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			_ = rdb.Close()
			a.closeClients()
			return nil, fmt.Errorf("ping redis: %w", err)
		}
		a.rdb = rdb
		limiter = mw.RateLimitShared(mw.NewRedisCounter(rdb), cfg.RatePerMinute, time.Minute)
	}

	app := fiber.New(fiber.Config{
		AppName:               "go-multitenant-gateway",
		DisableStartupMessage: true,
		ErrorHandler:          errorHandler,
	})

	app.Use(recover.New())
	modules.Mount(app, health.New())

	app.Use(tenantMW)
	app.Use(limiter)
	app.Use(mw.Auth(cfg.JWTSecret))

	modules.Mount(app, notesModule)

	a.Fiber = app
	return a, nil
}

// Listen serves on addr until the listener fails or Shutdown runs.
func (a *App) Listen(addr string) error {
	return a.Fiber.Listen(addr)
}

// Shutdown drains in-flight requests (bounded by ctx), then closes the pool
// and the Redis client. The order matters: handlers still running during the
// drain need their backends alive.
func (a *App) Shutdown(ctx context.Context) error {
	err := a.Fiber.ShutdownWithContext(ctx)
	a.closeClients()
	if err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

// closeClients releases whatever backends were opened. Nil-guarded so it is
// safe on partial init failure and after a Shutdown.
func (a *App) closeClients() {
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
	if a.rdb != nil {
		_ = a.rdb.Close()
		a.rdb = nil
	}
}
