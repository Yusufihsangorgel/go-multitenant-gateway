package notes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

// newNotesApp mounts the module behind a stub that resolves the tenant from
// the X-Tenant header and pins a fixed user, standing in for the tenant and
// auth middleware the server puts in front.
func newNotesApp(m Module) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		id := c.Get("X-Tenant")
		tenant.Set(c, tenant.Tenant{ID: id, Schema: tenant.SchemaFor(id)})
		c.Locals("userID", "user-1")
		return c.Next()
	})
	m.Register(app.Group(m.Prefix()))
	return app
}

type listResponse struct {
	Tenant string `json:"tenant"`
	User   string `json:"user"`
	Notes  []Note `json:"notes"`
}

func listNotes(t *testing.T, app *fiber.App, tenantID string) listResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/notes/", nil)
	req.Header.Set("X-Tenant", tenantID)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return out
}

func TestCreateThenListIsTenantScoped(t *testing.T) {
	app := newNotesApp(New())

	req := httptest.NewRequest(http.MethodPost, "/notes/", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant", "acme")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created Note
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created note: %v", err)
	}
	if created.ID == 0 || created.Text != "hello" || created.UserID != "user-1" || created.CreatedAt.IsZero() {
		t.Fatalf("created note = %+v, want id, text, user and timestamp set", created)
	}

	// The creating tenant sees its note.
	got := listNotes(t, app, "acme")
	if got.Tenant != "acme" || got.User != "user-1" {
		t.Fatalf("list envelope = %+v, want tenant acme, user user-1", got)
	}
	if len(got.Notes) != 1 || got.Notes[0].ID != created.ID || got.Notes[0].Text != "hello" {
		t.Fatalf("acme notes = %+v, want exactly the created note", got.Notes)
	}

	// Another tenant sees nothing: the store is keyed by schema.
	other := listNotes(t, app, "globex")
	if len(other.Notes) != 0 {
		t.Fatalf("globex notes = %+v, want empty", other.Notes)
	}
}

func TestCreateRejectsEmptyText(t *testing.T) {
	app := newNotesApp(New())

	for _, body := range []string{`{}`, `{"text":""}`, `not json`} {
		req := httptest.NewRequest(http.MethodPost, "/notes/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant", "acme")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("create request failed: %v", err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("create with body %q = %d, want %d", body, resp.StatusCode, http.StatusBadRequest)
		}
	}
}
