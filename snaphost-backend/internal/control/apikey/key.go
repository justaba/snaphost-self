// Package apikey issues and verifies long-lived API keys that let non-browser
// clients (MCP server, editor extension, CLI) authenticate without a browser
// session. Only the SHA-256 hash of a key is persisted; the plaintext secret is
// returned to the caller once at creation.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// keyPrefix is the human-visible marker identifying a SnapHost API key. The
// authentication middleware uses it to decide whether a bearer value is an API key
// or a credential it does not accept.
const keyPrefix = "sk_"

// secretBytes is the number of random bytes in a key's secret portion.
const secretBytes = 32

// displayPrefixLen is how many characters of the key (including keyPrefix) are
// kept as a non-secret display prefix.
const displayPrefixLen = len(keyPrefix) + 8

// Generated holds a freshly minted key: the plaintext (shown once), its stored
// hash, and its non-secret display prefix.
type Generated struct {
	Plaintext string
	Hash      string
	Prefix    string
}

// Generate creates a new random API key. The plaintext has the form
// "sk_<64 hex chars>" and must be shown to the user exactly once.
func Generate() (Generated, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return Generated{}, fmt.Errorf("generate api key: %w", err)
	}
	plaintext := keyPrefix + hex.EncodeToString(buf)
	return Generated{
		Plaintext: plaintext,
		Hash:      Hash(plaintext),
		Prefix:    plaintext[:displayPrefixLen],
	}, nil
}

// Hash returns the lowercase hex SHA-256 of a full key. Verification hashes the
// presented key and looks it up; the plaintext is never compared directly.
func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// HasKeyPrefix reports whether a bearer credential looks like a SnapHost API key
// rather than a JWT. Used by HTTP authentication middleware.
func HasKeyPrefix(cred string) bool {
	return strings.HasPrefix(cred, keyPrefix)
}
