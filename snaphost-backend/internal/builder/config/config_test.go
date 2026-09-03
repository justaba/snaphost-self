package config

import "testing"

func TestLoadHasUsableDefaults(t *testing.T) {
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

func TestStaleServiceConfigurationIsIgnored(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "")
	t.Setenv("AI_ORCHESTRATOR_URL", "://invalid")
	t.Setenv("USER_BILLING_URL", "://invalid")
	t.Setenv("REGISTRY_URL", "registry:5000/snaphost")
	t.Setenv("REGISTRY_INSECURE", "true")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
}
