package middleware

import (
	"context"
	"strings"
)

// apiKeyPrefix marks a bearer credential as a SnapHost API key. Must match the
// prefix minted by the control plane's apikey package.
const apiKeyPrefix = "sk_"

// IsAPIKey reports whether a bearer credential is a SnapHost API key.
func IsAPIKey(cred string) bool {
	return strings.HasPrefix(cred, apiKeyPrefix)
}

// KeyVerifier resolves an API key to its owning user id.
//
// An interface because the middleware must stay testable without a database.
// The implementation is a direct repository lookup; the HTTP client that used
// to sit behind it went when the gateway and the control plane became one
// process.
type KeyVerifier interface {
	Verify(ctx context.Context, key string) (string, error)
}
