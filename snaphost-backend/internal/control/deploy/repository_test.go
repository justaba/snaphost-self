package deploy

import (
	"strings"
	"testing"
)

func TestUpdateStatusSQLStoppedAtAccounting(t *testing.T) {
	required := []struct {
		name string
		want string
	}{
		{
			name: "stopped status sets stopped_at",
			want: "WHEN $1 = 'stopped' THEN COALESCE(stopped_at, now())",
		},
		{
			name: "non stopped statuses preserve stopped_at",
			want: "ELSE stopped_at",
		},
	}

	normalized := strings.Join(strings.Fields(updateStatusSQL), " ")
	for _, tc := range required {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(normalized, tc.want) {
				t.Fatalf("updateStatusSQL missing %q in %q", tc.want, normalized)
			}
		})
	}

	if strings.Contains(normalized, "'deleted'") {
		t.Fatalf("generic status update should not special-case deleted; MarkDeleted owns deleted stopped_at behavior: %q", normalized)
	}
}

func TestIsPlatformHost(t *testing.T) {
	r := &Repository{}
	WithDomainSuffix("Snaphost.PW")(r)

	cases := map[string]bool{
		"proj-abc.snaphost.pw": true,
		"shop.example.com":     false,
		"snaphost.pw":          false, // apex is not a deploy subdomain
		"snaphost.pw.evil.com": false, // suffix must be a real suffix, not a substring
	}
	for host, want := range cases {
		if got := r.isPlatformHost(host); got != want {
			t.Fatalf("isPlatformHost(%q) = %v, want %v", host, got, want)
		}
	}

	// With no suffix configured every host takes the legacy subdomain
	// branch, so an unset DOMAIN_SUFFIX is exactly the pre-16 behavior.
	if !(&Repository{}).isPlatformHost("shop.example.com") {
		t.Fatal("unset domain suffix must fall back to the subdomain branch")
	}
}

func TestCustomDomainRouteRequiresVerifiedAndRunning(t *testing.T) {
	normalized := strings.Join(strings.Fields(customDomainRouteSQL), " ")
	for _, want := range []string{
		"cd.status = 'verified'",
		"d.status = 'running'",
		"d.container_id <> ''",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("custom domain lookup missing %q: an unverified or dead alias must not route: %s", want, normalized)
		}
	}
}

func TestReclaimNeverTakesAnAliasedDeploy(t *testing.T) {
	normalized := strings.Join(strings.Fields(reclaimableSQL), " ")
	if !strings.Contains(normalized, "AND NOT EXISTS ( SELECT 1 FROM custom_domains cd") {
		t.Fatalf("reclaim sweep must exclude alias-pinned deploys: %s", normalized)
	}
	if !strings.Contains(normalized, "ttl_expires_at < now()") {
		t.Fatalf("reclaim sweep must still honor TTL expiry: %s", normalized)
	}
	if !strings.Contains(normalized, "rank > $2") {
		t.Fatalf("reclaim sweep must apply per-project retention: %s", normalized)
	}

	unpin := strings.Join(strings.Fields(unpinIdleAliasesSQL), " ")
	if !strings.Contains(unpin, "cd.status = 'verified'") || !strings.Contains(unpin, "target_deploy_id = NULL") {
		t.Fatalf("idle sweep must unpin the alias before its target can be reclaimed: %s", unpin)
	}
}

func TestNormalizeRouteHost(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		wantHost      string
		wantSubdomain string
	}{
		{
			name:          "lowercase and port",
			in:            "Proj-ABC.snaphost.pw:443",
			wantHost:      "proj-abc.snaphost.pw",
			wantSubdomain: "proj-abc",
		},
		{
			name:          "trim trailing dot",
			in:            "proj-abc.snaphost.pw.",
			wantHost:      "proj-abc.snaphost.pw",
			wantSubdomain: "proj-abc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHost, gotSubdomain, err := normalizeRouteHost(tc.in)
			if err != nil {
				t.Fatalf("normalizeRouteHost() error = %v", err)
			}
			if gotHost != tc.wantHost || gotSubdomain != tc.wantSubdomain {
				t.Fatalf("normalizeRouteHost() = (%q, %q), want (%q, %q)", gotHost, gotSubdomain, tc.wantHost, tc.wantSubdomain)
			}
		})
	}
}
