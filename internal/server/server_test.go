package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

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

func do(t *testing.T, method, path string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := New(testCfg).Test(req)
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
