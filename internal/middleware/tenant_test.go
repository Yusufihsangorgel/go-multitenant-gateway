package middleware

import (
	"context"
	"encoding/json"
	"errors"
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

// newLookupApp mounts TenantFrom with the given lookup in front of the same
// probe handler newTenantApp uses.
func newLookupApp(lookup SchemaLookup) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(TenantFrom(lookup))
	app.Get("/", func(c *fiber.Ctx) error {
		t, ok := tenant.From(c)
		if !ok {
			return fiber.NewError(fiber.StatusInternalServerError, "tenant missing from context")
		}
		return c.JSON(fiber.Map{"id": t.ID, "schema": t.Schema})
	})
	return app
}

func TestTenantFromResolvesSchemaThroughLookup(t *testing.T) {
	lookup := func(_ context.Context, id string) (string, bool, error) {
		if id != "acme" {
			t.Fatalf("lookup called with id %q, want %q", id, "acme")
		}
		// Deliberately not SchemaFor's convention: the schema on the context
		// must be what the registry said, not what the header implies.
		return "custom_schema", true, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "acme")
	resp, err := newLookupApp(lookup).Test(req)
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
	if got.ID != "acme" || got.Schema != "custom_schema" {
		t.Fatalf("tenant = %+v, want id=acme schema=custom_schema", got)
	}
}

func TestTenantFromRejectsUnregisteredTenant(t *testing.T) {
	lookup := func(context.Context, string) (string, bool, error) {
		return "", false, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "nosuch")
	resp, err := newLookupApp(lookup).Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got, want := string(body), "unknown tenant"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestTenantFromRejectsMissingHeader(t *testing.T) {
	called := false
	lookup := func(context.Context, string) (string, bool, error) {
		called = true
		return "", false, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := newLookupApp(lookup).Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if called {
		t.Fatal("lookup ran for a request with no tenant header")
	}
}

func TestTenantFromFailsClosedOnLookupError(t *testing.T) {
	lookup := func(context.Context, string) (string, bool, error) {
		return "", false, errors.New("registry unavailable")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant", "acme")
	resp, err := newLookupApp(lookup).Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (no schema means no safe way to serve)", resp.StatusCode, http.StatusInternalServerError)
	}
}
