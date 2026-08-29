package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDockerfile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func hasCode(issues []ValidationIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestValidate_ModeStrict_RejectsBunImage(t *testing.T) {
	path := writeDockerfile(t, "FROM oven/bun:1-alpine\nUSER 1000\nCMD [\"bun\",\"start\"]\n")
	issues, err := ValidateDockerfileWithOptions(path, Options{
		Mode:                               ModeStrict,
		AllowedBaseImagePrefixesStrict:     []string{"node:", "python:"},
		AllowedBaseImagePrefixesPermissive: []string{"oven/bun:", "node:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(issues, "base_image_not_allowed") {
		t.Errorf("strict mode must reject oven/bun, got issues: %+v", issues)
	}
}

func TestValidate_ModePermissive_AcceptsBunImage(t *testing.T) {
	path := writeDockerfile(t, "FROM oven/bun:1-alpine\nUSER 1000\nCMD [\"bun\",\"start\"]\n")
	issues, err := ValidateDockerfileWithOptions(path, Options{
		Mode:                               ModePermissive,
		AllowedBaseImagePrefixesStrict:     []string{"node:", "python:"},
		AllowedBaseImagePrefixesPermissive: []string{"oven/bun:", "node:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasCode(issues, "base_image_not_allowed") {
		t.Errorf("permissive mode must accept oven/bun:1-alpine, got: %+v", issues)
	}
}

func TestValidate_ModePermissive_FallbackToStrict(t *testing.T) {
	path := writeDockerfile(t, "FROM node:20-alpine\nUSER 1000\n")
	issues, err := ValidateDockerfileWithOptions(path, Options{
		Mode:                               ModePermissive,
		AllowedBaseImagePrefixesStrict:     []string{"node:"},
		AllowedBaseImagePrefixesPermissive: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasCode(issues, "base_image_not_allowed") {
		t.Errorf("permissive with empty list should fall back to strict (which allows node:), got: %+v", issues)
	}
}

func TestCheckBaseImageTag_MissingTag(t *testing.T) {
	if got := checkBaseImageTag("node", 1); got == nil || got.Code != "base_image_missing_tag" {
		t.Errorf("want base_image_missing_tag, got %+v", got)
	}
}

func TestCheckBaseImageTag_LatestTag(t *testing.T) {
	if got := checkBaseImageTag("node:latest", 1); got == nil || got.Code != "base_image_latest_tag" {
		t.Errorf("want base_image_latest_tag, got %+v", got)
	}
}

func TestCheckBaseImageTag_ExplicitVersion(t *testing.T) {
	if got := checkBaseImageTag("node:20-alpine", 1); got != nil {
		t.Errorf("explicit tag must pass, got %+v", got)
	}
}

func TestCheckBaseImageTag_DigestPin(t *testing.T) {
	if got := checkBaseImageTag("node@sha256:abc123", 1); got != nil {
		t.Errorf("digest pin must pass, got %+v", got)
	}
}

func TestCheckBaseImageTag_Scratch(t *testing.T) {
	if got := checkBaseImageTag("scratch", 1); got != nil {
		t.Errorf("scratch must pass, got %+v", got)
	}
}

func TestCheckBaseImageTag_RegistryHostWithPort(t *testing.T) {
	// "FROM host.docker.internal:5000/snaphost/proj-abc:v1" — colon in
	// registry host must NOT be mistaken for a tag separator.
	if got := checkBaseImageTag("host.docker.internal:5000/snaphost/proj-abc:v1", 1); got != nil {
		t.Errorf("registry-host-with-port must pass on explicit tag, got %+v", got)
	}
	// Same ref without tag must trigger missing-tag.
	if got := checkBaseImageTag("host.docker.internal:5000/snaphost/proj-abc", 1); got == nil || got.Code != "base_image_missing_tag" {
		t.Errorf("registry-host-with-port without tag must report missing tag, got %+v", got)
	}
}

func TestValidate_LatestTag_BothModes(t *testing.T) {
	path := writeDockerfile(t, "FROM node:latest\nUSER 1000\n")
	for _, mode := range []Mode{ModeStrict, ModePermissive} {
		issues, err := ValidateDockerfileWithOptions(path, Options{
			Mode:                               mode,
			AllowedBaseImagePrefixesStrict:     []string{"node:"},
			AllowedBaseImagePrefixesPermissive: []string{"node:"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !hasCode(issues, "base_image_latest_tag") {
			t.Errorf("mode=%v must reject :latest, got %+v", mode, issues)
		}
	}
}

// TestValidate_FullyQualifiedDockerHub is the regression for the tcgdex
// smoke test bug: a Dockerfile writing "FROM docker.io/oven/bun:1-alpine"
// must validate successfully when "oven/bun:" is in the permissive list.
// Pre-Task-9.4 this failed because prefix match was raw-string.
func TestValidate_FullyQualifiedDockerHub(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	contents := "FROM docker.io/oven/bun:1-alpine AS build\nUSER bun\n"
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}

	issues, err := ValidateDockerfileWithOptions(path, Options{
		Mode:                               ModePermissive,
		AllowedBaseImagePrefixesStrict:     []string{"node:"},
		AllowedBaseImagePrefixesPermissive: []string{"oven/bun:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range issues {
		if iss.Code == "base_image_not_allowed" {
			t.Fatalf("docker.io/oven/bun:1-alpine should be accepted with prefix oven/bun: in list; got %+v",
				iss)
		}
	}
}
