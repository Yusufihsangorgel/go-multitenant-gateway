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
	"log"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/config"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/server"
)

func main() {
	cfg := config.Load()
	app := server.New(cfg)

	log.Printf("gateway listening on :%s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatal(err)
	}
}
