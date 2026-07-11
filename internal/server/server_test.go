package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/config"
)

var testCfg = config.Config{JWTSecret: []byte("test-secret"), RatePerMinute: 1000}

// token builds a valid HS256 JWT for the test secret.
func token(sub string) string {
	enc := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := enc([]byte(`{"sub":"` + sub + `"}`))
	mac := hmac.New(sha256.New, testCfg.JWTSecret)
	mac.Write([]byte(header + "." + payload))
	return header + "." + payload + "." + enc(mac.Sum(nil))
}

// do builds the app with empty DATABASE_URL and REDIS_URL, so these tests
// exercise the same wiring the binary runs without touching any service.
func do(t *testing.T, method, path string, headers map[string]string) int {
	t.Helper()
	a, err := New(context.Background(), testCfg)
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.Fiber.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp.StatusCode
}

func TestHealthNeedsNoAuth(t *testing.T) {
	if got := do(t, http.MethodGet, "/health/", nil); got != http.StatusOK {
		t.Fatalf("health = %d, want 200", got)
	}
}

func TestTenantIsRequired(t *testing.T) {
	if got := do(t, http.MethodGet, "/notes/", nil); got != http.StatusBadRequest {
		t.Fatalf("no tenant = %d, want 400", got)
	}
}

func TestAuthIsRequired(t *testing.T) {
	h := map[string]string{"X-Tenant": "acme"}
	if got := do(t, http.MethodGet, "/notes/", h); got != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", got)
	}
}

func TestValidRequestPasses(t *testing.T) {
	h := map[string]string{"X-Tenant": "acme", "Authorization": "Bearer " + token("user-1")}
	if got := do(t, http.MethodGet, "/notes/", h); got != http.StatusOK {
		t.Fatalf("valid request = %d, want 200", got)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	h := map[string]string{"X-Tenant": "acme", "Authorization": "Bearer " + token("user-1") + "x"}
	if got := do(t, http.MethodGet, "/notes/", h); got != http.StatusUnauthorized {
		t.Fatalf("tampered token = %d, want 401", got)
	}
}

// TestErrorHandlerHidesBackendDetail pins the information-disclosure boundary:
// deliberate fiber errors keep their message, everything else (a store error,
// a panic recover turned into an error) becomes a generic 500. Backend error
// text carries schema names and SQL detail that must never reach a client.
func TestErrorHandlerHidesBackendDetail(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return fmt.Errorf(`list notes for tenant acme: relation "notes" does not exist (SQLSTATE 42P01)`)
	})
	app.Get("/known", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusNotFound, "unknown tenant")
	})

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("request %s failed: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return resp.StatusCode, string(body)
	}

	if code, body := get("/boom"); code != http.StatusInternalServerError || body != "internal error" {
		t.Fatalf("raw error surfaced as %d %q, want 500 %q", code, body, "internal error")
	}
	if code, body := get("/known"); code != http.StatusNotFound || body != "unknown tenant" {
		t.Fatalf("fiber error surfaced as %d %q, want 404 %q", code, body, "unknown tenant")
	}
}
