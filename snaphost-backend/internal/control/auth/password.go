// Package auth issues this platform's own identity.
//
// It replaces Supabase, which had exactly one job here: serving a JWKS the
// gateway fetched to verify the signature on somebody else's token. That is a
// multi-tenant SaaS's identity provider, and this platform has one operator —
// so keeping it meant the panel could not be logged into without either an
// external service or a ten-container self-hosted Supabase standing next to a
// 9 MiB binary.
//
// What replaces it is deliberately small: an argon2id password on a row in the
// users table, and an opaque session token in a table beside it.
//
// # Why a session table and not a self-signed JWT
//
// A locally signed JWT would have been fewer moving parts up front, and it was
// the obvious alternative. It loses on two behaviours that are not optional:
//
//   - Logout has to revoke. A stateless token stays valid until it expires, so
//     "log out" would mean "stop sending it" — which is not what an operator
//     pressing the button is asking for.
//   - Changing the password has to invalidate every other session. That is the
//     entire point of changing it after a laptop is lost.
//
// Both are achievable with a JWT, by keeping a denylist of tokens until they
// expire — which is this table, with a worse name and an extra signing key to
// generate, persist and rotate. The usual argument for the stateless token is
// that it saves a round trip to a session store; here the store is a file this
// process already has open, so the round trip does not exist.
//
// The cost is one indexed read on a local SQLite file per authenticated
// request, which is paid on the same handle every handler already uses.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id cost parameters.
//
// OWASP publishes five configurations it considers equivalent in strength,
// trading memory against iterations: m=47104/t=1, m=19456/t=2, m=12288/t=3,
// m=9216/t=4 and m=7168/t=5, all at p=1. This is the last of them — the same
// defence at 7 MiB instead of 46.
//
// Which one to take is not a close call for this platform. The memory a
// password hash reserves is the memory the fork exists to leave to the sites,
// and it was measured rather than assumed: at m=19456 three sequential logins
// took the process from 8 MiB resident to 65, and it did not come back. Go
// grows its heap to roughly twice what is live, so a 19 MiB transient becomes
// tens of megabytes of retained heap on a box picked for having a gigabyte.
//
// The parameters are written into every hash, so moving along that list later
// affects new passwords without invalidating old ones — verification reads the
// cost out of the stored string rather than assuming these values.
const (
	argonTime    = 5
	argonMemory  = 7 * 1024 // KiB
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// hashSlots bounds how many argon2id computations run at once.
//
// Each one reserves argonMemory while it runs, so without a bound a burst of
// login attempts is a memory amplifier: the IP rate limiter allows 30 requests
// a minute by default, and 30 concurrent hashes would be 210 MiB at these
// parameters. Two at a time caps it at 14 MiB, and logins are rare enough that
// queueing behind one costs nothing.
var hashSlots = make(chan struct{}, 2)

func withHashSlot(fn func()) {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	fn()
}

// MinPasswordLen is the shortest password accepted on a change. Twelve
// characters rather than eight: this credential guards a host that runs
// arbitrary containers as root, and it is typed by one person a few times a
// year.
const MinPasswordLen = 12

// MaxPasswordLen bounds what will be hashed at all. argon2 has no input-length
// limit of its own, so without one a megabyte-long body becomes a megabyte of
// hashing.
const MaxPasswordLen = 1024

// ErrPasswordTooShort and ErrPasswordTooLong report a rejected new password.
var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
)

// ErrPasswordMismatch reports a password that does not match the stored hash.
// It is deliberately the same error for a wrong password and for an account
// with no password at all, so the caller cannot tell the two apart.
var ErrPasswordMismatch = errors.New("password does not match")

// CheckPasswordPolicy validates a candidate password before it is hashed.
func CheckPasswordPolicy(password string) error {
	switch {
	case len(password) < MinPasswordLen:
		return ErrPasswordTooShort
	case len(password) > MaxPasswordLen:
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword returns a PHC-encoded argon2id hash:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
//
// The encoding is the standard one rather than a bare digest so that the cost
// parameters travel with the hash. A stored digest whose parameters live in a
// constant cannot be raised without locking everyone out.
func HashPassword(password string) (string, error) {
	if err := CheckPasswordPolicy(password); err != nil {
		return "", err
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	var key []byte
	withHashSlot(func() {
		key = argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	})

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether a password matches an encoded hash.
//
// An empty or malformed hash is ErrPasswordMismatch rather than a distinct
// error: the only caller is a login handler, and a row with no password must
// answer exactly as a wrong password does.
func VerifyPassword(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return ErrPasswordMismatch
	}
	if len(password) > MaxPasswordLen {
		return ErrPasswordMismatch
	}

	var got []byte
	withHashSlot(func() {
		got = argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	})

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// decodeHash reads the cost parameters, salt and key back out of a PHC string.
func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams

	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, errors.New("malformed hash")
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("unsupported algorithm %q", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, errors.New("malformed version")
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("unsupported argon2 version %d", version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, errors.New("malformed parameters")
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, errors.New("zero cost parameter")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, errors.New("malformed salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return p, nil, nil, errors.New("malformed key")
	}

	return p, salt, key, nil
}

// GeneratePassword returns a password for the first-start operator account.
//
// 18 bytes of crypto/rand rendered base64url: 144 bits, 24 characters, and no
// character a shell or a copy-paste out of a log will mangle. It is printed
// once and never stored in plaintext, so it has to survive being read off a
// terminal.
func GeneratePassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
