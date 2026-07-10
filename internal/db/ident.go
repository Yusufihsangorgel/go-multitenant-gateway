package db

import (
	"fmt"
	"regexp"
)

// identPattern is the allowlist for identifiers this gateway will interpolate
// into SQL. It is deliberately narrower than what Postgres accepts: lowercase
// ASCII letters, digits and underscores, starting with a letter or underscore.
// Anything a tenant registry should never contain (quotes, semicolons, mixed
// case, unicode) fails the match.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// QuoteIdent validates an identifier and returns it double-quoted for safe
// interpolation into SQL. Identifiers cannot be bound as query parameters, so
// statements like SET LOCAL search_path have to be built by string
// concatenation; this allowlist is what makes that safe. By the time a schema
// name reaches this function it was resolved from the tenant registry (or, at
// boot, derived from operator-provided config), not read off a request, but
// the check runs on every call anyway as a second line of defense.
func QuoteIdent(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("identifier is empty")
	}
	if len(name) > 63 {
		return "", fmt.Errorf("identifier %q exceeds 63 bytes", name)
	}
	if !identPattern.MatchString(name) {
		return "", fmt.Errorf("identifier %q contains characters outside [a-z0-9_]", name)
	}
	return `"` + name + `"`, nil
}
