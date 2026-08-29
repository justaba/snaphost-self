package config

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("SUPABASE_WEBHOOK_SECRET", "supabase-secret")
	t.Setenv("WEBHOOK_SECRET", "internal-secret")
}

func TestLoadKeepsWebhookSecretsSeparate(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SupabaseWebhookSecret != "supabase-secret" {
		t.Fatalf("SupabaseWebhookSecret = %q", cfg.SupabaseWebhookSecret)
	}
	if cfg.WebhookSecret != "internal-secret" {
		t.Fatalf("WebhookSecret = %q", cfg.WebhookSecret)
	}
}

func TestLoadRequiresWebhookSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("WEBHOOK_SECRET", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "WEBHOOK_SECRET") {
		t.Fatalf("Load() error = %v, want WEBHOOK_SECRET error", err)
	}
}

func TestLoadRequiresSupabaseWebhookSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SUPABASE_WEBHOOK_SECRET", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SUPABASE_WEBHOOK_SECRET") {
		t.Fatalf("Load() error = %v, want SUPABASE_WEBHOOK_SECRET error", err)
	}
}
