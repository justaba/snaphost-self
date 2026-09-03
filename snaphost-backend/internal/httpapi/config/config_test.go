package config

// Tests for the public HTTP server configuration.

import "testing"

func TestLoadNeedsNoEnvironment(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the default 8080", cfg.Port)
	}
}
