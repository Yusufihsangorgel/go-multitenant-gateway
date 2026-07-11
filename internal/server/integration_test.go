package server

// Integration test against a real Postgres, gated on TEST_DATABASE_URL like
// the other packages. It boots the full app the way the binary does and
// proves the serving-path claims end to end: seed tenants are registered,
// migrated and routable; a request for an unregistered tenant is rejected by
// the registry lookup before any SQL runs; and notes written through the
// gateway land in the tenant's own schema.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/tenant"
)

func TestServerServesRegisteredTenantsOnly(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	id := fmt.Sprintf("srv_%d", time.Now().UnixNano())
	schema := tenant.SchemaFor(id)

	cfg := testCfg
	cfg.DatabaseURL = url
	cfg.SeedTenants = []string{id}

	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("build app with database: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The app never listened, so Shutdown reports the server as not
		// running; what matters here is that it closes the pool.
		_ = a.Shutdown(shutdownCtx)

		pool, err := db.Connect(context.Background(), url, 2)
		if err != nil {
			t.Fatalf("connect for cleanup: %v", err)
		}
		defer pool.Close()
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.tenants WHERE id = $1", id)
		if quoted, err := db.QuoteIdent(schema); err == nil {
			_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		}
	})

	send := func(method, path, tenantID, body string) (int, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("X-Tenant", tenantID)
		req.Header.Set("Authorization", "Bearer "+token("user-1"))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := a.Fiber.Test(req, -1)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, path, err)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return resp.StatusCode, string(b)
	}

	// The seeded tenant was registered and migrated at boot, so a write and a
	// read through the whole chain must work against its schema.
	if code, body := send(http.MethodPost, "/notes/", id, `{"text":"through the gateway"}`); code != http.StatusCreated {
		t.Fatalf("create note = %d %q, want 201", code, body)
	}
	code, body := send(http.MethodGet, "/notes/", id, "")
	if code != http.StatusOK {
		t.Fatalf("list notes = %d %q, want 200", code, body)
	}
	var got struct {
		Notes []struct {
			Text string `json:"text"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode notes: %v", err)
	}
	if len(got.Notes) != 1 || got.Notes[0].Text != "through the gateway" {
		t.Fatalf("notes = %q, want exactly the created note", body)
	}

	// An unregistered tenant fails at the registry lookup with a 404. Before
	// the registry was on the serving path this reached the database and came
	// back as a 500 with backend detail; now no SQL runs at all.
	code, body = send(http.MethodGet, "/notes/", "not_registered", "")
	if code != http.StatusNotFound {
		t.Fatalf("unregistered tenant = %d %q, want 404", code, body)
	}
	if body != "unknown tenant" {
		t.Fatalf("unregistered tenant body = %q, want %q", body, "unknown tenant")
	}
}
