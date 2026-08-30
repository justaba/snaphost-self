package config

import "testing"

// The registry configuration is gone: REGISTRY_URL, REGISTRY_USERNAME,
// REGISTRY_PASSWORD, REGISTRY_INSECURE and REGISTRY_AUTH_MODE, plus the
// auto-detection that turned Trivy's --insecure on for a local registry.
//
// The tests that were here checked that auto-detection. They are replaced
// rather than deleted, because what they were guarding — that a build can be
// configured at all, and that the one required variable is required — is still
// worth a test.
func TestLoadRequiresOnlyTheWebhookSecret(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BuildKitHost == "" {
		t.Error("BuildKitHost has no default")
	}
	if cfg.WorkdirRoot == "" {
		t.Error("WorkdirRoot has no default")
	}
}

func TestLoadRefusesAMissingWebhookSecret(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an empty WEBHOOK_SECRET")
	}
}

// A leftover REGISTRY_URL in someone's env file must not change anything. It
// is not read, and a config that silently honoured it would be worse than one
// that ignores it.
func TestAStaleRegistryURLIsIgnored(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "test-secret")
	t.Setenv("REGISTRY_URL", "registry:5000/snaphost")
	t.Setenv("REGISTRY_INSECURE", "true")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
}
