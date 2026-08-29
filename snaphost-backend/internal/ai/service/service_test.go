package service

import (
	"testing"

	"snaphost/internal/ai/config"
)

// TestLLMConstraintsUseConfigAllowList is a regression guard against
// re-introducing a hardcoded base image list. The whole point of Task 9.2
// was to make ai-orchestrator share the same env-driven list as builder-svc.
// If this test fails, someone replaced s.cfg.AllowedBaseImagePrefixes with
// a literal slice in service.go.
func TestLLMConstraintsUseConfigAllowList(t *testing.T) {
	cfg := &config.Config{
		AllowedBaseImagePrefixes: []string{"custom-vendor:", "node:"},
	}
	svc := &Service{cfg: cfg}

	got := svc.buildLLMConstraints()
	if len(got.AllowedBaseImagePrefixes) != 2 {
		t.Fatalf("expected 2 prefixes from config, got %d: %v",
			len(got.AllowedBaseImagePrefixes), got.AllowedBaseImagePrefixes)
	}
	if got.AllowedBaseImagePrefixes[0] != "custom-vendor:" {
		t.Errorf("constraint allow-list should pass through config verbatim, got %v",
			got.AllowedBaseImagePrefixes)
	}
	if !got.RequireNonRootUser {
		t.Error("RequireNonRootUser must remain true")
	}
}
