package router

import (
	"net/http"
	"testing"
)

func TestStripCookieDomain(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "drops the shared-domain scope",
			in:   "sid=abc; Path=/; Domain=snaphost.pw; HttpOnly; Secure",
			want: "sid=abc; Path=/; HttpOnly; Secure",
		},
		{
			name: "case and spacing do not hide the attribute",
			in:   "sid=abc;domain=.snaphost.pw ; Path=/",
			want: "sid=abc; Path=/",
		},
		{
			name: "host-only cookies pass through untouched",
			in:   "sid=abc; Path=/; SameSite=Lax",
			want: "sid=abc; Path=/; SameSite=Lax",
		},
		{
			name: "a value that merely looks like the attribute is kept",
			in:   "domain=example.com; Path=/",
			want: "domain=example.com; Path=/",
		},
		{
			name: "unknown attributes are preserved",
			in:   "sid=abc; Domain=snaphost.pw; Partitioned; Priority=High",
			want: "sid=abc; Partitioned; Priority=High",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripCookieDomain(tc.in); got != tc.want {
				t.Fatalf("stripCookieDomain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestConfineCookiesToHostRewritesDeployResponses(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "a=1; Domain=snaphost.pw; Path=/")
	header.Add("Set-Cookie", "b=2; Path=/")

	confineCookiesToHost(header, "proj-a.snaphost.pw", "snaphost.pw")

	got := header.Values("Set-Cookie")
	want := []string{"a=1; Path=/", "b=2; Path=/"}
	if len(got) != len(want) {
		t.Fatalf("Set-Cookie count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Set-Cookie[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A customer's own domain has no other tenant on it, and sharing a cookie with
// their own www is legitimate. Rewriting it would break working sites.
func TestConfineCookiesToHostLeavesCustomDomainsAlone(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "sid=1; Domain=example.com; Path=/")

	confineCookiesToHost(header, "shop.example.com", "snaphost.pw")

	if got := header.Get("Set-Cookie"); got != "sid=1; Domain=example.com; Path=/" {
		t.Fatalf("custom-domain cookie was rewritten: %q", got)
	}
}

// The suffix apex is not a deploy hostname, and an unset suffix must not turn
// the proxy into something that rewrites every response it sees.
func TestConfineCookiesToHostIgnoresNonDeployHosts(t *testing.T) {
	for _, tc := range []struct{ host, suffix string }{
		{"snaphost.pw", "snaphost.pw"},
		{"proj-a.snaphost.pw", ""},
	} {
		header := http.Header{}
		header.Add("Set-Cookie", "sid=1; Domain=snaphost.pw; Path=/")

		confineCookiesToHost(header, tc.host, tc.suffix)

		if got := header.Get("Set-Cookie"); got != "sid=1; Domain=snaphost.pw; Path=/" {
			t.Fatalf("host=%q suffix=%q: cookie was rewritten: %q", tc.host, tc.suffix, got)
		}
	}
}
