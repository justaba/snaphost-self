package project

import (
	"strings"
	"testing"
)

func TestGitSourceKeyIsStableAcrossURLForms(t *testing.T) {
	same := []string{
		"https://GitHub.com/acme/Site.git",
		"https://github.com/acme/Site",
		"https://github.com/acme/Site/",
	}
	want := GitSourceKey(same[0], "main")
	for _, u := range same[1:] {
		if got := GitSourceKey(u, "main"); got != want {
			t.Fatalf("GitSourceKey(%q) = %q, want %q", u, got, want)
		}
	}
	// Repo path case is preserved in neither direction; only the branch
	// distinguishes otherwise-identical sources.
	if GitSourceKey(same[0], "main") == GitSourceKey(same[0], "dev") {
		t.Fatal("different branches must not share a project")
	}
}

func TestArchiveSourceKeyIsPerDeploy(t *testing.T) {
	a := ArchiveSourceKey("11111111-1111-1111-1111-111111111111")
	b := ArchiveSourceKey("22222222-2222-2222-2222-222222222222")
	if a == b {
		t.Fatal("archive deploys must not share a project: an upload carries no stable identity")
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		name       string
		sourceKey  string
		wantPrefix string
	}{
		{"git repo name", "git:github.com/acme/my-site#main", "my-site-"},
		{"underscores and case folded", "git:github.com/acme/My_Site#main", "my-site-"},
		{"archive", "archive:11111111-1111-1111-1111-111111111111", "11111111-"},
		{"empty base falls back", "git:#main", "project-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Slug(tc.sourceKey)
			if len(got) == 0 {
				t.Fatal("slug must not be empty")
			}
			if tc.wantPrefix != "" && got[:len(tc.wantPrefix)] != tc.wantPrefix {
				t.Fatalf("Slug(%q) = %q, want prefix %q", tc.sourceKey, got, tc.wantPrefix)
			}
		})
	}

	if Slug("git:github.com/a/site#main") == Slug("git:github.com/b/site#main") {
		t.Fatal("same repo name from different owners must not collide")
	}
	key := "git:github.com/acme/site#main"
	if Slug(key) != Slug(GitSourceKey("https://github.com/acme/site.git", "main")) {
		t.Fatal("slug must be deterministic for the same source across URL forms")
	}
	if len(Slug("git:github.com/acme/"+longName(120)+"#main")) > maxSlugLen+7 {
		t.Fatal("slug must stay bounded")
	}
}

func longName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// --- client-supplied project identity -------------------------------------
// Without this, every archive upload becomes its own project, so a custom
// domain attached to one can never follow a later build — which makes custom
// domains unusable on exactly the path the MCP server and editor extension
// take.

func TestClientSourceKeyIsNamespaced(t *testing.T) {
	// A key shaped like a git source must not land where a git deploy would.
	got := ClientSourceKey("git:github.com/someone/repo#main")
	if got == GitSourceKey("https://github.com/someone/repo", "main") {
		t.Fatal("a client key must not be able to collide with a derived git key")
	}
	if !strings.HasPrefix(got, "client:") {
		t.Fatalf("key = %q, want a client: prefix", got)
	}
}

func TestClientSourceKeyIsStableAcrossDeploys(t *testing.T) {
	// The whole point: the same key resolves to the same project every time,
	// unlike ArchiveSourceKey which is deliberately per-deploy.
	if ClientSourceKey("my-app") != ClientSourceKey("  My-App  ") {
		t.Fatal("the same key with different casing or padding must resolve identically")
	}
	if ArchiveSourceKey("deploy-1") == ArchiveSourceKey("deploy-2") {
		t.Fatal("archive keys are per-deploy by design; this test guards that contrast")
	}
}

func TestValidateClientProjectKey(t *testing.T) {
	valid := []string{
		"my-app",
		"3f2a1c88-1e2b-4a5c-9f00-1122334455aa",
		"workspace/api",
		"a.b_c-d:e",
		strings.Repeat("k", MaxClientProjectKey),
	}
	for _, k := range valid {
		if code := ValidateClientProjectKey(k); code != "" {
			t.Errorf("ValidateClientProjectKey(%q) = %q, want accepted", k, code)
		}
	}

	rejected := map[string]string{
		"":          "invalid_project_key",
		"   ":       "invalid_project_key",
		"has space": "invalid_project_key",
		"emoji-🙂":   "invalid_project_key",
		"new\nline": "invalid_project_key",
		strings.Repeat("k", MaxClientProjectKey+1): "project_key_too_long",
	}
	for k, want := range rejected {
		if code := ValidateClientProjectKey(k); code != want {
			t.Errorf("ValidateClientProjectKey(%q) = %q, want %q", k, code, want)
		}
	}
}
