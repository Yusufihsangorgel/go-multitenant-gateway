// Command gateway is a single-binary, multi-tenant API gateway reference.
//
// One process serves many tenants and many product modules. Each request runs
// through the same middleware chain — recover, tenant resolution, rate limit,
// auth — and is then handled by whichever module owns its path prefix.
//
// This mirrors, in miniature, a gateway I run in production that serves a fleet
// of apps from one binary. The real one has many more modules and routes; this
// keeps the pattern and drops the product code. See ARCHITECTURE.md.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/config"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/server"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := server.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- app.Listen(":" + cfg.Port) }()
	log.Printf("gateway listening on :%s", cfg.Port)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal(err)
		}
	case <-ctx.Done():
		// A signal arrived. Drain in-flight requests for up to 10 seconds,
		// then close the database pool and the Redis client.
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Shutdown(shutdownCtx); err != nil {
			log.Fatal(err)
		}
	}
}
