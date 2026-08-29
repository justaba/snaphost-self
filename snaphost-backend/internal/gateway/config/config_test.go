package config

import "testing"

// Load has no required variables left. It used to refuse to start without
// SUPABASE_URL and two webhook secrets; the first is gone with the identity
// provider, and the secret that still matters is required by the control
// plane's config, which is the half that reads it.
func TestLoadNeedsNoEnvironment(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the default 8080", cfg.Port)
	}
	if cfg.MaxUploadSizeMB != 50 {
		t.Errorf("MaxUploadSizeMB = %d, want the default 50", cfg.MaxUploadSizeMB)
	}
}

// A malformed integer falls back to the default rather than failing. That is
// the inherited behaviour and it is deliberate here: an unparseable upload
// ceiling must not become an unbounded one.
func TestMalformedIntFallsBackToTheDefault(t *testing.T) {
	t.Setenv("MAX_UPLOAD_SIZE_MB", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxUploadSizeMB != 50 {
		t.Errorf("MaxUploadSizeMB = %d, want the default 50", cfg.MaxUploadSizeMB)
	}
}
