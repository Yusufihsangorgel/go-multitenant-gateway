package middleware

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// Tenant resolves which tenant a request belongs to and stores it on the
// context. Here it reads the "X-Tenant" header; in production the tenant
// usually comes from the subdomain or the verified JWT claims, but the shape
// is the same: resolve once, at the edge, and let everything downstream trust
// the context.
//
// This variant derives the schema by naming convention and is the default for
// the in-memory quickstart, where no SQL ever runs. With Postgres on, the
// server wires TenantFrom instead, so the schema comes from the registry.
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

// SchemaLookup resolves a tenant ID to its Postgres schema. found is false
// when the tenant is not registered; err means the lookup itself failed.
type SchemaLookup func(ctx context.Context, id string) (schema string, found bool, err error)

// TenantFrom resolves the tenant like Tenant does, but the schema comes from
// a lookup against the tenant registry instead of a naming convention. The
// header still names the tenant; the schema its queries will run in is
// whatever the registry holds for that tenant. An unregistered ID is rejected
// here with 404, so no request input ever reaches SQL as an identifier. A
// failed lookup fails closed: without a schema there is no safe way to serve
// the request.
func TenantFrom(lookup SchemaLookup) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get("X-Tenant")
		if id == "" {
			return fiber.NewError(fiber.StatusBadRequest, "missing tenant")
		}
		schema, found, err := lookup(c.UserContext(), id)
		if err != nil {
			return fmt.Errorf("resolve tenant %s: %w", id, err)
		}
		if !found {
			return fiber.NewError(fiber.StatusNotFound, "unknown tenant")
		}
		tenant.Set(c, tenant.Tenant{ID: id, Schema: schema})
		return c.Next()
	}
}
