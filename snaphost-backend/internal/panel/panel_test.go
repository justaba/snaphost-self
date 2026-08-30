package panel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>panel</title>")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("body{}")},
	}
}

// A checkout that has never run the frontend build must still compile and
// start. The embedded directory holds only a .gitkeep in that state.
func TestAssetsReportsAnUnbuiltPanel(t *testing.T) {
	if _, err := Assets(); err != nil && err != ErrNotBuilt {
		t.Fatalf("Assets() error = %v, want nil or ErrNotBuilt", err)
	}
}

// The route a client-side router owns does not exist on the server. Answering
// 404 would break every page reload and every shared link.
func TestUnknownPathsGetTheIndex(t *testing.T) {
	h := Handler(testAssets())

	for _, path := range []string{"/", "/deploys", "/deploys/8b1f0c2e-0000-4000-8000-000000000000", "/login"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		if got := rec.Body.String(); got != "<!doctype html><title>panel</title>" {
			t.Errorf("GET %s served %q, want index.html", path, got)
		}
	}
}

// An asset that exists is served as itself, not as the shell. Getting this
// wrong is subtle: the page loads, and every script tag silently receives HTML.
func TestExistingFilesAreServedDirectly(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(testAssets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil))

	if body := rec.Body.String(); body != "console.log(1)" {
		t.Fatalf("asset body = %q, want the file itself", body)
	}
}

// A mistyped API path must 404. If the SPA fallback answered it, a client
// would receive 200 and a page of HTML where it expected JSON, which is far
// harder to diagnose than a missing route.
func TestAPIPathsAreNeverServedTheIndex(t *testing.T) {
	h := Handler(testAssets())

	for _, path := range []string{"/api/v1/deploys", "/api/v1/nonexistent", "/ws/logs/x", "/internal/deploys", "/health", "/metrics"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

// The middleware must let everything it does not own through untouched, or it
// would swallow the API it is mounted in front of.
func TestMiddlewarePassesThroughWhatItDoesNotOwn(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"panel route", http.MethodGet, "/deploys", true},
		{"asset", http.MethodGet, "/assets/app-abc123.js", true},
		{"head is allowed", http.MethodHead, "/", true},
		{"api read", http.MethodGet, "/api/v1/deploys", false},
		{"api write", http.MethodPost, "/api/v1/deploys", false},
		{"a POST to a panel path is not the panel's", http.MethodPost, "/login", false},
		{"websocket", http.MethodGet, "/ws/logs/x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serves(httptest.NewRequest(tc.method, tc.path, nil))
			if got != tc.want {
				t.Fatalf("serves(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// The shell names hashed bundles, so a cached copy of it keeps an upgraded
// panel loading the previous build's assets.
func TestTheIndexIsNotCached(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(testAssets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

// A path that climbs out of the asset tree must not reach the filesystem. The
// fallback makes this quieter than usual: an escape that resolves to nothing
// returns the index rather than an error, so nothing about the response says a
// traversal was attempted.
func TestTraversalDoesNotEscapeTheAssets(t *testing.T) {
	h := Handler(testAssets())

	for _, path := range []string{"/../go.mod", "/assets/../../go.mod", "/%2e%2e/go.mod"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if body := rec.Body.String(); body != "<!doctype html><title>panel</title>" && rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want the index or 404", path, rec.Code, body)
		}
	}
}
