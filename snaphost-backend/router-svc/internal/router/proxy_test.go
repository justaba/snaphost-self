package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeLookup struct {
	route *Route
	calls int
	host  string
}

func (f *fakeLookup) Lookup(_ context.Context, host string) (*Route, error) {
	f.calls++
	f.host = host
	if f.route == nil {
		return nil, ErrRouteNotFound
	}
	return f.route, nil
}

type fakeResolver struct{ url string }

func (f fakeResolver) ResolveURL(context.Context, string) (string, error) { return f.url, nil }

type fakeTokens struct{}

func (fakeTokens) Token(context.Context) (string, error) { return "iam-token", nil }

func TestProxyPreservesRequestAndResponse(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotBody, gotAuth, gotForwardedHost, gotConnection string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotForwardedHost = r.Header.Get("X-Forwarded-Host")
		gotConnection = r.Header.Get("Connection")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("X-Upstream", "ok")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("deploy A"))
	}))
	defer upstream.Close()

	lookup := &fakeLookup{route: &Route{
		DeployID:    "deploy-a",
		ContainerID: "container-a",
		Status:      "running",
		Host:        "proj-a.snaphost.pw",
	}}
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       lookup,
		Resolver:     fakeResolver{url: upstream.URL},
		Tokens:       fakeTokens{},
		Client:       upstream.Client(),
	}

	req := httptest.NewRequest(http.MethodPost, "https://proj-a.snaphost.pw/path?q=1", strings.NewReader("request body"))
	req.Host = "Proj-A.snaphost.pw:443"
	req.Header.Set("Connection", "keep-alive")
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "deploy A" {
		t.Fatalf("body = %q, want deploy A", w.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/path" || gotQuery != "q=1" || gotBody != "request body" {
		t.Fatalf("upstream request mismatch: method=%s path=%s query=%s body=%q", gotMethod, gotPath, gotQuery, gotBody)
	}
	if gotAuth != "Bearer iam-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotForwardedHost != "proj-a.snaphost.pw" {
		t.Fatalf("X-Forwarded-Host = %q", gotForwardedHost)
	}
	if lookup.host != "proj-a.snaphost.pw" {
		t.Fatalf("lookup host = %q", lookup.host)
	}
	if gotConnection != "" {
		t.Fatalf("hop-by-hop Connection header leaked upstream: %q", gotConnection)
	}
	if w.Header().Get("X-Upstream") != "ok" {
		t.Fatalf("response header not copied")
	}
	if w.Header().Get("Connection") != "" {
		t.Fatalf("hop-by-hop response Connection header leaked: %q", w.Header().Get("Connection"))
	}
}

// End-to-end wiring check: a deploy that tries to scope a cookie to the shared
// suffix must not be able to reach another user's deploy with it.
func TestProxyConfinesDeployCookiesToTheirHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "sid=stolen; Domain=snaphost.pw; Path=/")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup: &fakeLookup{route: &Route{
			DeployID:    "deploy-a",
			ContainerID: "container-a",
			Status:      "running",
			Host:        "proj-a.snaphost.pw",
		}},
		Resolver: fakeResolver{url: upstream.URL},
		Tokens:   fakeTokens{},
		Client:   upstream.Client(),
	}
	req := httptest.NewRequest(http.MethodGet, "https://proj-a.snaphost.pw/", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	got := w.Header().Get("Set-Cookie")
	if got != "sid=stolen; Path=/" {
		t.Fatalf("Set-Cookie = %q, want the Domain attribute removed", got)
	}
}

func TestProxyRouteNotFound(t *testing.T) {
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       &fakeLookup{},
		Resolver:     fakeResolver{url: "https://example.test"},
		Tokens:       fakeTokens{},
	}
	req := httptest.NewRequest(http.MethodGet, "https://missing.snaphost.pw/", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestLookupClientReturnsRouteNotFound(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "http not found", statusCode: http.StatusNotFound, body: `{"error":"route_not_found"}`},
		{name: "non running route", statusCode: http.StatusOK, body: `{"deploy_id":"d","container_id":"c","status":"stopped","host":"proj-a.snaphost.pw"}`},
		{name: "empty container", statusCode: http.StatusOK, body: `{"deploy_id":"d","container_id":"","status":"running","host":"proj-a.snaphost.pw"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			client := &LookupClient{BaseURL: srv.URL, Secret: "secret", HTTPClient: srv.Client()}
			route, err := client.Lookup(context.Background(), "proj-a.snaphost.pw")
			if route != nil {
				t.Fatalf("route = %+v, want nil", route)
			}
			if !errors.Is(err, ErrRouteNotFound) {
				t.Fatalf("err = %v, want ErrRouteNotFound", err)
			}
		})
	}
}

// A host outside our suffix is a custom domain (Task 16d): it is resolved by
// full hostname instead of being rejected. Whether it serves is user-billing's
// call — only a verified alias resolves there.
func TestProxyLooksUpForeignHostByFullHostname(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	lookup := &fakeLookup{route: &Route{
		DeployID:    "deploy-a",
		ContainerID: "container-a",
		Status:      "running",
		Host:        "shop.example.com",
	}}
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       lookup,
		Resolver:     fakeResolver{url: upstream.URL},
		Tokens:       fakeTokens{},
		Client:       upstream.Client(),
	}
	req := httptest.NewRequest(http.MethodGet, "https://shop.example.com/", nil)
	req.Host = "Shop.Example.com:443"
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if lookup.host != "shop.example.com" {
		t.Fatalf("lookup host = %q, want the full normalized hostname", lookup.host)
	}
}

func TestProxyUnknownForeignHostIs404(t *testing.T) {
	lookup := &fakeLookup{}
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       lookup,
		Resolver:     fakeResolver{url: "https://example.test"},
		Tokens:       fakeTokens{},
	}
	req := httptest.NewRequest(http.MethodGet, "https://not-attached.example.com/", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (same as an unknown subdomain)", w.Code)
	}
}

// The platform's own namespace keeps its stricter shape: a nested host under
// our suffix was never a deploy and must not become a custom-domain lookup.
func TestProxyRejectsMalformedPlatformHost(t *testing.T) {
	lookup := &fakeLookup{}
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       lookup,
		Resolver:     fakeResolver{url: "https://example.test"},
		Tokens:       fakeTokens{},
	}
	req := httptest.NewRequest(http.MethodGet, "https://a.b.snaphost.pw/", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup was called %d times for a malformed platform host", lookup.calls)
	}
}

func TestProxyRejectsApexHost(t *testing.T) {
	lookup := &fakeLookup{}
	proxy := &Proxy{
		DomainSuffix: "snaphost.pw",
		Lookup:       lookup,
		Resolver:     fakeResolver{url: "https://example.test"},
		Tokens:       fakeTokens{},
	}
	req := httptest.NewRequest(http.MethodGet, "https://snaphost.pw/", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if lookup.calls != 0 {
		t.Fatalf("apex host must not reach lookup")
	}
}

func TestNormalizeHost(t *testing.T) {
	got, err := NormalizeHost("Proj-A.snaphost.pw:443")
	if err != nil {
		t.Fatal(err)
	}
	if got != "proj-a.snaphost.pw" {
		t.Fatalf("NormalizeHost() = %q", got)
	}
}

func TestNormalizeHostForDomain(t *testing.T) {
	cases := []struct {
		name          string
		host          string
		domainSuffix  string
		wantHost      string
		wantSubdomain string
	}{
		{
			name:          "valid",
			host:          "proj-a.snaphost.pw",
			domainSuffix:  "snaphost.pw",
			wantHost:      "proj-a.snaphost.pw",
			wantSubdomain: "proj-a",
		},
		{
			name:          "port",
			host:          "proj-a.snaphost.pw:443",
			domainSuffix:  "snaphost.pw",
			wantHost:      "proj-a.snaphost.pw",
			wantSubdomain: "proj-a",
		},
		{
			name:          "case and trailing dot",
			host:          "Proj-A.Snaphost.PW.",
			domainSuffix:  "Snaphost.PW.",
			wantHost:      "proj-a.snaphost.pw",
			wantSubdomain: "proj-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHost, gotSubdomain, err := NormalizeHostForDomain(tc.host, tc.domainSuffix)
			if err != nil {
				t.Fatalf("NormalizeHostForDomain() error = %v", err)
			}
			if gotHost != tc.wantHost || gotSubdomain != tc.wantSubdomain {
				t.Fatalf("NormalizeHostForDomain() = (%q, %q), want (%q, %q)", gotHost, gotSubdomain, tc.wantHost, tc.wantSubdomain)
			}
		})
	}
}

func TestNormalizeHostForDomainRejectsInvalidHosts(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{name: "foreign", host: "proj-a.attacker.example"},
		{name: "bare domain", host: "snaphost.pw"},
		{name: "nested subdomain", host: "proj-a.extra.snaphost.pw"},
		{name: "empty", host: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if gotHost, gotSubdomain, err := NormalizeHostForDomain(tc.host, "snaphost.pw"); err == nil {
				t.Fatalf("NormalizeHostForDomain() = (%q, %q), want error", gotHost, gotSubdomain)
			}
		})
	}
}
