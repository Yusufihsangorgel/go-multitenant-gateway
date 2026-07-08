package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

var authSecret = []byte("test-signing-secret")

// newAuthApp mounts the Auth middleware in front of a handler that echoes the
// user id the middleware pulled off the context, so a test can prove the `sub`
// claim actually landed in Locals rather than only that the request passed.
func newAuthApp(secret []byte) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Auth(secret))
	app.Get("/", func(c *fiber.Ctx) error {
		uid, _ := c.Locals("userID").(string)
		return c.SendString(uid)
	})
	return app
}

// signHS256 mints a valid HS256 token with the given secret, using the same
// library the middleware verifies with.
func signHS256(t *testing.T, secret []byte, sub string) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": sub}).SignedString(secret)
	if err != nil {
		t.Fatalf("sign HS256: %v", err)
	}
	return signed
}

func TestAuthRejectsBadCredentials(t *testing.T) {
	app := newAuthApp(authSecret)

	// A token signed with a different secret must fail signature verification.
	wrongKey := signHS256(t, []byte("a-different-secret"), "user-1")

	// A structurally valid RS256 token must also be rejected: the keyfunc only
	// trusts *SigningMethodHMAC, which is what defends against the classic
	// alg-swap attack (handing the HMAC verifier an RS256/"none" token).
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	rs256, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "user-1"}).SignedString(rsaKey)
	if err != nil {
		t.Fatalf("sign RS256: %v", err)
	}

	cases := []struct {
		name       string
		authHeader string // "" means send no Authorization header at all
	}{
		{"no authorization header", ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"missing bearer prefix", signHS256(t, authSecret, "user-1")},
		{"malformed token", "Bearer not-a-jwt"},
		{"signed with wrong key", "Bearer " + wrongKey},
		{"non-hmac algorithm", "Bearer " + rs256},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestAuthAcceptsValidTokenAndSetsUserID(t *testing.T) {
	app := newAuthApp(authSecret)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signHS256(t, authSecret, "user-42"))

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got, want := string(body), "user-42"; got != want {
		t.Fatalf("userID on context = %q, want %q", got, want)
	}
}
