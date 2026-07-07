// Package tenant carries the resolved tenant through the request lifecycle.
//
// The whole gateway is multi-tenant: one binary serves many tenants, and each
// request is scoped to exactly one. Downstream code (DB queries, rate limits,
// caches) reads the tenant from the request context instead of parsing it
// again. Data isolation is done at the Postgres *schema* level — every tenant
// gets its own schema in one shared database, so there is one connection pool
// and one backup, not one database per tenant. See ARCHITECTURE.md for why.
package tenant

import "github.com/gofiber/fiber/v2"

// localsKey is the fiber Locals key under which the tenant is stored.
const localsKey = "tenant"

// Tenant is the minimal identity a request is scoped to.
type Tenant struct {
	ID     string // stable tenant identifier (e.g. "acme")
	Schema string // Postgres schema this tenant's data lives in
}

// Set stores the tenant on the request context.
func Set(c *fiber.Ctx, t Tenant) {
	c.Locals(localsKey, t)
}

// From returns the tenant on the request context and whether it was present.
func From(c *fiber.Ctx) (Tenant, bool) {
	t, ok := c.Locals(localsKey).(Tenant)
	return t, ok
}

// SchemaFor maps a tenant ID to its Postgres schema name. Kept trivial here on
// purpose; in production this is a validated lookup so a header can never point
// a query at an arbitrary schema.
func SchemaFor(id string) string {
	return "tenant_" + id
}
