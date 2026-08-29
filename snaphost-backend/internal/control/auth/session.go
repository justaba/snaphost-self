package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// CookieName is the session cookie. Named for the product rather than the
// framework so it does not collide with a cookie a deployed site sets — deploys
// live on a different registrable domain for that reason, but the local dev
// path puts them under one suffix.
const CookieName = "snaphost_session"

// sessionTokenBytes is the entropy in a session token. 32 bytes is the same
// budget an API key gets, and for the same reason: it is the only secret
// between a stolen cookie and the whole control plane.
const sessionTokenBytes = 32

// DefaultTTL is how long a session lives when nothing configures it.
const DefaultTTL = 7 * 24 * time.Hour

// NewToken mints a session token and returns the plaintext to put in the cookie
// alongside the hash to store. The plaintext is never persisted.
func NewToken() (plaintext, hash string, err error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashToken(plaintext), nil
}

// HashToken returns the lowercase hex SHA-256 of a session token. Lookups hash
// what was presented and search for that; the plaintext is never compared.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
