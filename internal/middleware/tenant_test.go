package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// newTenantApp mounts the Tenant middleware in front of a probe handler that
// echoes back whatever tenant the middleware stored on the context. That lets a
// test assert both the HTTP status and the value that actually reached
// downstream handlers, not just that the request was allowed through.
func newTenantApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Tenant())
	app.Get("/", func(c *fiber.Ctx) error {
		t, ok := tenant.From(c)
		if !ok {
			// The middleware must not call Next without a tenant on the
			// context; surface that as a distinct failure if it ever does.
			return fiber.NewError(fiber.StatusInternalServerError, "tenant missing from context")
		}
		return c.JSON(fiber.Map{"id": t.ID, "schema": t.Schema})
	})
	return app
}

func TestTenantRejectsMissingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := newTenantApp().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got, want := string(body), "missing tenant"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestTenantResolvesAndStoresOnContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "acme")

	resp, err := newTenantApp().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var got struct {
		ID     string `json:"id"`
		Schema string `json:"schema"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ID != "acme" {
		t.Errorf("tenant id = %q, want %q", got.ID, "acme")
	}
	// The middleware derives the Postgres schema via tenant.SchemaFor; assert the
	// mapping the header actually produced rather than trusting the ID alone.
	if want := tenant.SchemaFor("acme"); got.Schema != want {
		t.Errorf("tenant schema = %q, want %q", got.Schema, want)
	}
}
