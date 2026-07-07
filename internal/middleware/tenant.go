package middleware

import (
	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// Tenant resolves which tenant a request belongs to and stores it on the
// context. Here it reads the "X-Tenant" header; in production the tenant
// usually comes from the subdomain or the verified JWT claims, but the shape
// is the same: resolve once, at the edge, and let everything downstream trust
// the context.
//
// A request without a tenant is rejected — there is no "default" tenant, and
// letting one through would risk querying the wrong schema.
func Tenant() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get("X-Tenant")
		if id == "" {
			return fiber.NewError(fiber.StatusBadRequest, "missing tenant")
		}
		tenant.Set(c, tenant.Tenant{ID: id, Schema: tenant.SchemaFor(id)})
		return c.Next()
	}
}
