package cache

import (
	"testing"

	"snaphost/internal/ai/llm"
)

// newTestRepo returns a Repository with a nil pool. ComputeSignature does
// not touch the pool, so this is safe for pure-hash testing.
func newTestRepo() *Repository {
	return &Repository{}
}

func TestComputeSignature_Deterministic(t *testing.T) {
	r := newTestRepo()
	req := llm.GenerateRequest{
		FileTree: []string{"a.txt", "b.txt"},
		KeyFiles: map[string]string{"package.json": `{"name":"x"}`},
	}
	a := r.ComputeSignature(req)
	b := r.ComputeSignature(req)
	if a != b {
		t.Fatalf("ComputeSignature not deterministic: %q != %q", a, b)
	}
}

func TestComputeSignature_FileOrderIndependent(t *testing.T) {
	r := newTestRepo()
	a := r.ComputeSignature(llm.GenerateRequest{
		FileTree: []string{"a.txt", "b.txt", "c.txt"},
	})
	b := r.ComputeSignature(llm.GenerateRequest{
		FileTree: []string{"c.txt", "a.txt", "b.txt"},
	})
	if a != b {
		t.Errorf("file order should not affect signature: %q vs %q", a, b)
	}
}

func TestComputeSignature_KeyOrderIndependent(t *testing.T) {
	r := newTestRepo()
	a := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{"package.json": "{}", ".nvmrc": "16"},
	})
	b := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{".nvmrc": "16", "package.json": "{}"},
	})
	if a != b {
		t.Errorf("key order should not affect signature: %q vs %q", a, b)
	}
}

func TestComputeSignature_ContentSensitive(t *testing.T) {
	r := newTestRepo()
	a := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{"package.json": `{"name":"x"}`},
	})
	b := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{"package.json": `{"name":"y"}`},
	})
	if a == b {
		t.Errorf("content change must change signature, both = %q", a)
	}
}

// Regression: ensure key-name vs key-content boundary cannot be confused.
// Without a delimiter, KeyFiles{"a": "bc"} and KeyFiles{"ab": "c"} would
// hash to the same input bytes ("abc"). With the delimiter they differ.
func TestComputeSignature_KeyBoundaryRegression(t *testing.T) {
	r := newTestRepo()
	a := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{"a": "bc"},
	})
	b := r.ComputeSignature(llm.GenerateRequest{
		KeyFiles: map[string]string{"ab": "c"},
	})
	if a == b {
		t.Errorf("key-name vs content boundary collision, both = %q", a)
	}
}

// Canary: if a future refactor accidentally drops the version mixin, the
// empty-request signature would degrade to sha256("") — fail loudly.
func TestComputeSignature_VersionIsMixed(t *testing.T) {
	r := newTestRepo()
	empty := r.ComputeSignature(llm.GenerateRequest{})
	const sha256OfEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if empty == sha256OfEmpty {
		t.Fatal("cacheSchemaVersion was not mixed into the signature")
	}
}
