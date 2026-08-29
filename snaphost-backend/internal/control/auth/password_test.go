package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	const password = "correct horse battery"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Fatalf("VerifyPassword on the right password: %v", err)
	}
	if err := VerifyPassword(hash, password+"x"); err == nil {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

// Two hashes of the same password must differ, or the salt is not being used
// and the store has become a rainbow-table lookup.
func TestHashIsSalted(t *testing.T) {
	const password = "correct horse battery"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of the same password are identical; the salt is not random")
	}
}

// The cost parameters have to travel inside the hash. If they are ever read
// from the constants instead, raising them locks every existing account out —
// and nothing would report that until someone tried to log in.
func TestHashCarriesItsCostParameters(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=7168,t=5,p=1$") {
		t.Fatalf("hash = %q, want a PHC string carrying m, t and p", hash)
	}

	params, salt, key, err := decodeHash(hash)
	if err != nil {
		t.Fatalf("decodeHash: %v", err)
	}
	if params.memory != argonMemory || params.time != argonTime || params.threads != argonThreads {
		t.Fatalf("decoded params = %+v, want m=%d t=%d p=%d", params, argonMemory, argonTime, argonThreads)
	}
	if len(salt) != argonSaltLen || len(key) != argonKeyLen {
		t.Fatalf("salt/key lengths = %d/%d, want %d/%d", len(salt), len(key), argonSaltLen, argonKeyLen)
	}
}

// A password hashed under a different point on the cost curve still verifies.
// This is the claim that makes the parameters above changeable: they were moved
// from m=19456,t=2 to m=7168,t=5 after measuring what the first cost in
// resident memory, and every password already stored had to keep working.
func TestAHashWrittenAtOtherParametersStillVerifies(t *testing.T) {
	const password = "correct horse battery"

	salt := []byte("sixteen-byte-slt")
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, argonKeyLen)
	older := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, 19*1024, 2, 1,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)

	if err := VerifyPassword(older, password); err != nil {
		t.Fatalf("a hash written at m=19456,t=2 no longer verifies: %v", err)
	}
	if err := VerifyPassword(older, password+"x"); err == nil {
		t.Fatal("the older-parameter path accepted a wrong password")
	}
}

// A row with no password must answer exactly as a wrong password does. An
// account that cannot log in has an empty hash, and the difference between
// "wrong password" and "malformed stored hash" is not the client's business.
func TestVerifyRejectsMalformedHashesAsMismatches(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2id$unusable",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=13$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$a2V5",
	} {
		if err := VerifyPassword(encoded, "correct horse battery"); err != ErrPasswordMismatch {
			t.Errorf("VerifyPassword(%q) = %v, want ErrPasswordMismatch", encoded, err)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := CheckPasswordPolicy(strings.Repeat("a", MinPasswordLen-1)); err != ErrPasswordTooShort {
		t.Errorf("short password = %v, want ErrPasswordTooShort", err)
	}
	if err := CheckPasswordPolicy(strings.Repeat("a", MinPasswordLen)); err != nil {
		t.Errorf("password at the minimum = %v, want nil", err)
	}
	if err := CheckPasswordPolicy(strings.Repeat("a", MaxPasswordLen+1)); err != ErrPasswordTooLong {
		t.Errorf("long password = %v, want ErrPasswordTooLong", err)
	}
}

// The generated password is what an operator reads out of a log line and types
// once. It has to satisfy the policy it will be checked against later, or the
// first password change would be impossible to perform correctly.
func TestGeneratedPasswordSatisfiesThePolicy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		password, err := GeneratePassword()
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if err := CheckPasswordPolicy(password); err != nil {
			t.Fatalf("generated password %q fails the policy: %v", password, err)
		}
		if seen[password] {
			t.Fatalf("GeneratePassword repeated %q", password)
		}
		seen[password] = true
	}
}

// Tokens are compared by hash, so the hash has to be deterministic and the
// plaintext has to be unique.
func TestNewTokenIsUniqueAndHashesStably(t *testing.T) {
	first, firstHash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	second, secondHash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if first == second {
		t.Fatal("NewToken returned the same token twice")
	}
	if firstHash == secondHash {
		t.Fatal("NewToken returned the same hash twice")
	}
	if HashToken(first) != firstHash {
		t.Fatal("HashToken disagrees with the hash NewToken returned")
	}
	if strings.Contains(firstHash, first) {
		t.Fatal("the stored hash contains the plaintext token")
	}
}
