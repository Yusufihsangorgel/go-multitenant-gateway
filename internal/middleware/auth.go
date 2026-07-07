package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// Auth verifies the bearer token and puts the subject (user id) on the context.
//
// This reference uses an HMAC (HS256) shared secret to stay runnable with a
// single env var. In production the gateway verifies RS256 tokens against the
// auth service's public keys (JWKS) instead of sharing a secret — the gateway
// then holds no signing material and the auth service can rotate keys without
// touching the gateway. The verification point stays here either way: auth is a
// gateway concern, checked once, before any handler runs.
func Auth(secret []byte) fiber.Handler {
	const prefix = "Bearer "
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) {
			return fiber.NewError(fiber.StatusUnauthorized, "missing bearer token")
		}
		raw := strings.TrimPrefix(auth, prefix)

		token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return secret, nil
		})
		if err != nil || !token.Valid {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid token")
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if sub, ok := claims["sub"].(string); ok {
				c.Locals("userID", sub)
			}
		}
		return c.Next()
	}
}
