// Package health is a trivial module used to show the registration pattern and
// to give load balancers something to poll.
package health

import "github.com/gofiber/fiber/v2"

type Module struct{}

func New() Module { return Module{} }

func (Module) Prefix() string { return "/health" }

func (Module) Register(r fiber.Router) {
	r.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
}
