// Package middleware authenticates and enriches public HTTP requests.
package middleware

import (
	"context"
	"strings"
)

// apiKeyPrefix marks a bearer credential as a SnapHost API key. Must match the
// prefix minted by the apikey package.
const apiKeyPrefix = "sk_"

// IsAPIKey reports whether a bearer credential is a SnapHost API key.
func IsAPIKey(cred string) bool {
	return strings.HasPrefix(cred, apiKeyPrefix)
}

// KeyVerifier resolves an API key to its owning user id.
//
// An interface because the middleware must stay testable without a database.
// The implementation is a direct repository lookup.
type KeyVerifier interface {
	Verify(ctx context.Context, key string) (string, error)
}
