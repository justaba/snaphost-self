package apikey

import (
	"strings"
	"testing"
)

func TestGenerateProducesDistinctPrefixedKeys(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		g, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !strings.HasPrefix(g.Plaintext, "sk_") {
			t.Fatalf("plaintext missing sk_ prefix: %q", g.Plaintext)
		}
		if !HasKeyPrefix(g.Plaintext) {
			t.Fatalf("HasKeyPrefix false for minted key")
		}
		if g.Hash == g.Plaintext {
			t.Fatal("hash must not equal plaintext")
		}
		if got := Hash(g.Plaintext); got != g.Hash {
			t.Fatalf("Hash not stable: %q vs %q", got, g.Hash)
		}
		if !strings.HasPrefix(g.Plaintext, g.Prefix) {
			t.Fatalf("display prefix %q not a prefix of key", g.Prefix)
		}
		if len(g.Prefix) >= len(g.Plaintext) {
			t.Fatal("display prefix must not reveal the whole key")
		}
		if seen[g.Hash] {
			t.Fatal("duplicate key hash generated")
		}
		seen[g.Hash] = true
	}
}

func TestHashDiffersPerInput(t *testing.T) {
	if Hash("sk_a") == Hash("sk_b") {
		t.Fatal("distinct keys hashed to the same value")
	}
}

func TestHasKeyPrefixRejectsJWT(t *testing.T) {
	if HasKeyPrefix("eyJhbGciOi.header.sig") {
		t.Fatal("JWT misclassified as api key")
	}
}
