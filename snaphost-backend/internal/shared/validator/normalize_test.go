package validator

import "testing"

func TestNormalizeImageRef(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Short official images
		{"node:20", "docker.io/library/node:20"},
		{"node", "docker.io/library/node"},
		{"nginx:alpine", "docker.io/library/nginx:alpine"},
		{"postgres:15.3", "docker.io/library/postgres:15.3"},

		// Vendor namespace without registry
		{"oven/bun:1-alpine", "docker.io/oven/bun:1-alpine"},
		{"denoland/deno:latest", "docker.io/denoland/deno:latest"},
		{"library/node:20", "docker.io/library/node:20"},

		// Fully-qualified Docker Hub (the form that broke before this task)
		{"docker.io/oven/bun:1-alpine", "docker.io/oven/bun:1-alpine"},
		{"docker.io/library/node:20", "docker.io/library/node:20"},

		// Third-party registries
		{"gcr.io/distroless/base:debug", "gcr.io/distroless/base:debug"},
		{"gcr.io/distroless/base", "gcr.io/distroless/base"},
		{"ghcr.io/owner/image:v1", "ghcr.io/owner/image:v1"},
		{"quay.io/jetstack/cert-manager:v1", "quay.io/jetstack/cert-manager:v1"},
		{"mcr.microsoft.com/dotnet/sdk:8.0", "mcr.microsoft.com/dotnet/sdk:8.0"},

		// Local registries with port (regression-critical for snaphost dev stack)
		{"host.docker.internal:5000/snaphost/proj-abc:v1",
			"host.docker.internal:5000/snaphost/proj-abc:v1"},
		{"registry:5000/foo:bar", "registry:5000/foo:bar"},
		{"localhost:5000/foo", "localhost:5000/foo"},
		{"localhost/foo", "localhost/foo"},
		{"127.0.0.1:5000/x", "127.0.0.1:5000/x"},

		// Specials
		{"scratch", "scratch"},

		// Digest-pinned
		{"node@sha256:abc", "docker.io/library/node@sha256:abc"},
		{"node:20@sha256:abc", "docker.io/library/node:20@sha256:abc"},
		{"docker.io/oven/bun:1@sha256:abc", "docker.io/oven/bun:1@sha256:abc"},
		{"gcr.io/distroless/base@sha256:def", "gcr.io/distroless/base@sha256:def"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := normalizeImageRef(c.in); got != c.want {
				t.Errorf("normalizeImageRef(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeAllowedPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Short official images become library-namespaced
		{"node:", "docker.io/library/node:"},
		{"python:", "docker.io/library/python:"},
		{"nginx:", "docker.io/library/nginx:"},

		// Vendor without registry
		{"oven/bun:", "docker.io/oven/bun:"},
		{"denoland/deno:", "docker.io/denoland/deno:"},

		// Already-qualified
		{"docker.io/oven/bun:", "docker.io/oven/bun:"},
		{"gcr.io/distroless/", "gcr.io/distroless/"},
		{"mcr.microsoft.com/dotnet/", "mcr.microsoft.com/dotnet/"},

		// Specials
		{"scratch", "scratch"},
		{"", ""},

		// Case folding
		{"Node:", "docker.io/library/node:"},
		{"OVEN/BUN:", "docker.io/oven/bun:"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := normalizeAllowedPrefix(c.in); got != c.want {
				t.Errorf("normalizeAllowedPrefix(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestAllowListMatchAfterNormalization is the regression scenario for
// Task 9.4: prefix in env can be written either way, and image in
// Dockerfile can be written either way — all four combinations must
// converge to the same match result.
func TestAllowListMatchAfterNormalization(t *testing.T) {
	scenarios := []struct {
		name        string
		image       string
		prefix      string
		shouldMatch bool
	}{
		// Bun — the original tcgdex case
		{"bun_short_image_short_prefix",
			"oven/bun:1-alpine", "oven/bun:", true},
		{"bun_full_image_short_prefix",
			"docker.io/oven/bun:1-alpine", "oven/bun:", true},
		{"bun_short_image_full_prefix",
			"oven/bun:1-alpine", "docker.io/oven/bun:", true},
		{"bun_full_image_full_prefix",
			"docker.io/oven/bun:1-alpine", "docker.io/oven/bun:", true},

		// Library — same four-way symmetry
		{"node_short_image_short_prefix",
			"node:20-alpine", "node:", true},
		{"node_full_image_short_prefix",
			"docker.io/library/node:20-alpine", "node:", true},

		// Third-party registry — must NOT match docker.io prefix
		{"distroless_does_not_match_node",
			"gcr.io/distroless/base", "node:", false},
		{"unknown_vendor_does_not_match",
			"malicious/x:1", "oven/bun:", false},
		{"unknown_registry_does_not_match",
			"evil.example.com/x:1", "oven/bun:", false},

		// Local registry — never accidentally matches docker.io entries
		{"local_registry_not_matched_by_short",
			"host.docker.internal:5000/foo:v1", "node:", false},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			normalized := []string{normalizeAllowedPrefix(s.prefix)}
			got := isAllowedBaseImage(s.image, normalized)
			if got != s.shouldMatch {
				t.Errorf("image=%q prefix=%q: got match=%v, want %v",
					s.image, s.prefix, got, s.shouldMatch)
			}
		})
	}
}
