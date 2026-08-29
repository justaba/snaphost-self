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
	if cfg.RateLimitIP != 30 {
		t.Errorf("RateLimitIP = %d, want the default 30", cfg.RateLimitIP)
	}
}

// A malformed integer falls back to the default rather than failing. That is
// the inherited behaviour and it is deliberate for a rate limit: an unparseable
// value must not leave the limiter off.
func TestMalformedIntFallsBackToTheDefault(t *testing.T) {
	t.Setenv("RATE_LIMIT_IP", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RateLimitIP != 30 {
		t.Errorf("RateLimitIP = %d, want the default 30", cfg.RateLimitIP)
	}
}
