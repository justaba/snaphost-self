package validator

import "strings"

// normalizeImageRef converts a Docker image reference to its canonical form
// per the OCI Image Spec namespacing rules:
//
//   - Short names ("node:20") expand to "docker.io/library/<name>".
//   - Vendor names without registry ("oven/bun:1") expand to "docker.io/<name>".
//   - Fully-qualified references ("gcr.io/distroless/base", "docker.io/oven/bun:1")
//     are returned unchanged.
//   - "scratch" is a special reserved name — returned unchanged.
//   - Digest suffix (@sha256:...) is preserved.
//
// A reference is considered "fully qualified" if its first path segment
// contains "." or ":" or is exactly "localhost" — that's the same
// heuristic Docker / containerd / BuildKit use to distinguish a registry
// host from a Docker Hub namespace.
//
// This is intentionally NOT a full reference parser (no validation of
// allowed characters, length limits, etc.) — we only need canonical
// namespacing for prefix matching. A malformed input passes through
// without panic; downstream allow-list match will simply fail.
func normalizeImageRef(ref string) string {
	if ref == "scratch" {
		return ref
	}

	// Split off digest suffix; restored at the end. Digest doesn't affect
	// namespacing — "node@sha256:abc" canonicalizes to
	// "docker.io/library/node@sha256:abc".
	var digest string
	if i := strings.Index(ref, "@"); i != -1 {
		digest = ref[i:]
		ref = ref[:i]
	}

	slash := strings.Index(ref, "/")
	if slash == -1 {
		// "node:20" -> "docker.io/library/node:20"
		return "docker.io/library/" + ref + digest
	}

	firstPart := ref[:slash]
	if strings.ContainsAny(firstPart, ".:") || firstPart == "localhost" {
		// Already a registry host. "gcr.io/distroless/base",
		// "docker.io/oven/bun:1", "host.docker.internal:5000/foo:v1",
		// "localhost:5000/foo", "localhost/foo".
		return ref + digest
	}

	// Vendor namespace without registry. "oven/bun:1-alpine",
	// "denoland/deno:latest", "library/node:20".
	return "docker.io/" + ref + digest
}

// normalizeAllowedPrefix canonicalizes an allow-list prefix. Prefixes have
// trailing markers that need preservation:
//
//   - "node:"     — match any tag of node
//   - "gcr.io/distroless/"  — match anything under that registry path
//
// The trailing ":" or "/" indicates "match the prefix; remainder is
// arbitrary." We re-attach it after normalization.
//
// Example: "oven/bun:" -> "docker.io/oven/bun:".
//
// Empty or unrecognized inputs pass through unchanged. The match below is
// case-insensitive, so we lowercase here once instead of at each call.
func normalizeAllowedPrefix(prefix string) string {
	if prefix == "" {
		return prefix
	}

	trailing := ""
	stem := prefix
	switch prefix[len(prefix)-1] {
	case ':':
		trailing = ":"
		stem = prefix[:len(prefix)-1]
	case '/':
		trailing = "/"
		stem = prefix[:len(prefix)-1]
	}

	// "scratch" with no trailing marker is a special-case allow-list entry
	// — passes only the exact image "scratch".
	if stem == "scratch" && trailing == "" {
		return "scratch"
	}

	// To reuse normalizeImageRef we briefly pretend the stem is a full
	// ref. We MUST NOT add a fake tag/digest because the normalizer treats
	// "node" without slash as "library/node", which is what we want.
	// However we must avoid mis-classifying a stem that ends in a slash —
	// e.g. "gcr.io/distroless" (without trailing /) is already registry.
	// normalizeImageRef handles that correctly because "gcr.io" contains
	// a dot, so it stays as-is.
	normalizedStem := normalizeImageRef(stem)
	return strings.ToLower(normalizedStem + trailing)
}
