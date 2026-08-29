// Package project owns the permanent layer above a deploy (Task 16a).
//
// A deploy is one immutable build with its own URL and a TTL; a project is
// owner-scoped and permanent, and is what a custom domain binds to so the
// binding survives a redeploy. Deploys of the same source converge on the
// same project through a deterministic source key.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Project is a permanent, owner-scoped publish target.
type Project struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Slug      string    `json:"slug"`
	SourceKey string    `json:"source_key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// maxSlugLen bounds the derived slug; the 6-char hash suffix is added on top.
const maxSlugLen = 40

// GitSourceKey is the deterministic project key for a git deploy. The same
// repo on a different branch is a different publish target, so the branch is
// part of the key.
func GitSourceKey(repoURL, branch string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil || u.Host == "" {
		// Unparseable URLs still need a stable key; use the raw string.
		return "git:" + strings.ToLower(strings.TrimSpace(repoURL)) + "#" + branch
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	return "git:" + strings.ToLower(u.Host+"/"+path) + "#" + branch
}

// ArchiveSourceKey is the fallback project key for an uploaded archive. A bare
// upload carries no identity across deploys, so each one gets its own project
// rather than silently joining an unrelated one.
//
// That default makes a custom domain useless on the archive path: the domain
// belongs to a project, every upload is a new project, and so the alias can
// never be moved to a newer build. Clients that can remember something between
// deploys should send a ClientSourceKey instead.
func ArchiveSourceKey(deployID string) string {
	return "archive:" + deployID
}

// MaxClientProjectKey bounds a client-supplied key. Generous enough for a UUID
// or a path-derived slug, short enough not to become a storage channel.
const MaxClientProjectKey = 128

// ClientSourceKey namespaces a client-supplied project key.
//
// The prefix is what keeps the key space honest: without it a client could
// present a key shaped like "git:github.com/…" and land in the same project a
// git deploy would resolve to. Keys are already scoped per user by the unique
// index, so this is about the shape of the namespace rather than about one user
// reaching another's project.
func ClientSourceKey(key string) string {
	return "client:" + strings.ToLower(strings.TrimSpace(key))
}

// ValidateClientProjectKey checks a client-supplied key and returns a stable
// reason code, or "" when it is acceptable.
//
// Deliberately narrow: the key is opaque to us and only has to survive being a
// database value and a slug source. Anything a client might reasonably
// generate — a UUID, a hash, a folder name — passes.
func ValidateClientProjectKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "invalid_project_key"
	}
	if len(key) > MaxClientProjectKey {
		return "project_key_too_long"
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':', r == '/':
		default:
			return "invalid_project_key"
		}
	}
	return ""
}

// Slug derives a readable per-user identifier from the source key. The hash
// suffix keeps two different sources from colliding on the same repo name.
func Slug(sourceKey string) string {
	base := sourceKey
	if i := strings.LastIndex(base, "#"); i >= 0 {
		base = base[:i]
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.Index(base, ":"); i >= 0 && !strings.Contains(base, "/") {
		base = base[i+1:]
	}

	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(base) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "project"
	}
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}

	sum := sha256.Sum256([]byte(sourceKey))
	return fmt.Sprintf("%s-%s", slug, hex.EncodeToString(sum[:])[:6])
}
