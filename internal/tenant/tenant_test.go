package tenant

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestSchemaFor(t *testing.T) {
	cases := map[string]string{
		"acme":   "tenant_acme",
		"globex": "tenant_globex",
	}
	for id, want := range cases {
		if got := SchemaFor(id); got != want {
			t.Errorf("SchemaFor(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestSetFromRoundTrip runs Set and From inside a real handler because that is
// the only place a fiber ctx lives. From before Set must report absence, not a
// zero-value tenant that looks real.
func TestSetFromRoundTrip(t *testing.T) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", func(c *fiber.Ctx) error {
		if _, ok := From(c); ok {
			t.Error("From before Set reported a tenant")
		}

		want := Tenant{ID: "acme", Schema: SchemaFor("acme")}
		Set(c, want)

		got, ok := From(c)
		if !ok {
			t.Error("From after Set reported no tenant")
		}
		if got != want {
			t.Errorf("From = %+v, want %+v", got, want)
		}
		return c.SendStatus(http.StatusOK)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
