// Package modules defines how a product plugs into the gateway.
//
// The real motivation: one operator, many products. Each product is a Module
// that registers its own routes under its own path prefix. Adding a product is
// adding a Module and listing it once — no new service, no new deploy. That is
// the whole point of the single-binary design: the marginal cost of a new
// product is close to zero.
package modules

import "github.com/gofiber/fiber/v2"

// Module is one product's slice of the gateway.
type Module interface {
	// Prefix is the path all of this module's routes hang off, e.g. "/notes".
	Prefix() string
	// Register mounts the module's routes onto the given router group.
	Register(r fiber.Router)
}

// Mount registers every module under its prefix on the app.
func Mount(app *fiber.App, mods ...Module) {
	for _, m := range mods {
		m.Register(app.Group(m.Prefix()))
	}
}
