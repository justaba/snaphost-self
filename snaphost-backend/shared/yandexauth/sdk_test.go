package yandexauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// TestNewSDK_ConstructsWithoutNetwork verifies that NewSDK constructs a
// non-nil SDK from a syntactically valid authorized-key JSON without
// making any network calls. The IAM exchange happens lazily on first
// SDK method invocation; this test only covers the construction path.
func TestNewSDK_ConstructsWithoutNetwork(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	pubBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	keyJSON := map[string]string{
		"id":                 "fake-key-id",
		"service_account_id": "fake-sa-id",
		"key_algorithm":      "RSA_2048",
		"private_key":        string(privPEM),
		"public_key":         string(pubPEM),
	}
	raw, err := json.Marshal(keyJSON)
	if err != nil {
		t.Fatalf("marshal key json: %v", err)
	}

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.json")
	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	sdk, err := NewSDK(context.Background(), keyPath)
	if err != nil {
		t.Fatalf("NewSDK: %v", err)
	}
	if sdk == nil {
		t.Fatal("NewSDK returned nil SDK")
	}
}

func TestNewSDK_MissingFile(t *testing.T) {
	_, err := NewSDK(context.Background(), filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
}
