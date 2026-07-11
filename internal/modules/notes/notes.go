// Package notes is an example product module. It exists to show what a real
// module looks like: a prefix, a few routes, and handlers that read the tenant
// and user from the request context instead of re-parsing them. By default it
// keeps notes in process memory so the reference runs with no services; wire a
// Postgres-backed store (the server does when DATABASE_URL is set) and the
// same handlers run against the tenant's schema.
package notes

import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

type Module struct {
	store Store
}

// New returns the module backed by the in-memory store, the zero-dependency
// default.
func New() Module { return Module{store: newMemStore()} }

// NewWithStore returns the module backed by the given store. The server uses
// this to swap in the Postgres store without touching any handler.
func NewWithStore(s Store) Module { return Module{store: s} }

func (Module) Prefix() string { return "/notes" }

func (m Module) Register(r fiber.Router) {
	r.Get("/", m.list)
	r.Post("/", m.create)
}

// list reads the tenant's notes. The handler never decides the schema itself,
// it trusts the tenant already on the context, and the request context flows
// into the store so a client disconnect cancels the query.
func (m Module) list(c *fiber.Ctx) error {
	t, ok := tenant.From(c)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "tenant not resolved")
	}
	notes, err := m.store.List(c.UserContext(), t.Schema)
	if err != nil {
		return fmt.Errorf("list notes for tenant %s: %w", t.ID, err)
	}
	return c.JSON(fiber.Map{
		"tenant": t.ID,
		"user":   c.Locals("userID"),
		"notes":  notes,
	})
}

func (m Module) create(c *fiber.Ctx) error {
	var body struct {
		Text string `json:"text"`
	}
	if err := c.BodyParser(&body); err != nil || body.Text == "" {
		return fiber.NewError(fiber.StatusBadRequest, "text is required")
	}
	t, ok := tenant.From(c)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "tenant not resolved")
	}
	userID, _ := c.Locals("userID").(string)

	note, err := m.store.Create(c.UserContext(), t.Schema, userID, body.Text)
	if err != nil {
		return fmt.Errorf("create note for tenant %s: %w", t.ID, err)
	}
	return c.Status(fiber.StatusCreated).JSON(note)
}
