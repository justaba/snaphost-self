package config

import (
	"strings"
	"testing"
)

func TestOptionalLLMConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, key, wantError string
		wantEnabled                   bool
	}{
		{name: "no provider configured"},
		{name: "stored key does not opt in", key: "test-secret"},
		{name: "explicitly disabled", enabled: "false", key: "test-secret"},
		{name: "explicitly enabled", enabled: "true", key: "test-secret", wantEnabled: true},
		{name: "enabled without key", enabled: "true", wantError: "OPENROUTER_API_KEY is required when LLM_ENABLED=true"},
		{name: "blank key", enabled: "true", key: " \t", wantError: "OPENROUTER_API_KEY is required when LLM_ENABLED=true"},
		{name: "invalid switch", enabled: "typo", key: "test-secret", wantError: "LLM_ENABLED must be true or false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LLM_ENABLED", tc.enabled)
			t.Setenv("OPENROUTER_API_KEY", tc.key)
			t.Setenv("ALLOWED_BASE_IMAGES", "node:,nginx:")
			cfg, err := Load()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Load() error = %v, want %q", err, tc.wantError)
				}
				if strings.Contains(err.Error(), "test-secret") {
					t.Fatal("configuration error exposes the API key")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LLMEnabled != tc.wantEnabled {
				t.Fatalf("LLMEnabled = %v, want %v", cfg.LLMEnabled, tc.wantEnabled)
			}
		})
	}
}
