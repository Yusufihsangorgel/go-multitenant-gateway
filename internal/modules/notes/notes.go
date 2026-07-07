// Package notes is an example product module. It does nothing interesting on
// purpose — it exists to show what a real module looks like: a prefix, a few
// routes, and handlers that read the tenant and user from the request context
// instead of re-parsing them.
package notes

import (
	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

type Module struct{}

func New() Module { return Module{} }

func (Module) Prefix() string { return "/notes" }

func (Module) Register(r fiber.Router) {
	r.Get("/", list)
	r.Post("/", create)
}

// list would query `SELECT ... FROM <tenant schema>.notes`. The handler never
// decides the schema itself — it trusts the tenant already on the context.
func list(c *fiber.Ctx) error {
	t, ok := tenant.From(c)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "tenant not resolved")
	}
	// Demo response. A real handler would run the query against t.Schema.
	return c.JSON(fiber.Map{
		"tenant": t.ID,
		"schema": t.Schema,
		"user":   c.Locals("userID"),
		"notes":  []string{}, // would come from the tenant's schema
	})
}

func create(c *fiber.Ctx) error {
	var body struct {
		Text string `json:"text"`
	}
	if err := c.BodyParser(&body); err != nil || body.Text == "" {
		return fiber.NewError(fiber.StatusBadRequest, "text is required")
	}
	t, _ := tenant.From(c)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"tenant": t.ID,
		"text":   body.Text,
	})
}
