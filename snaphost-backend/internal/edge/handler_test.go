package edge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type fakeResolver struct {
	route *Route
	err   error
	asked []string
}

func (f *fakeResolver) Resolve(_ context.Context, host string) (*Route, error) {
	f.asked = append(f.asked, host)
	return f.route, f.err
}

func TestTLSAskAllowsOnlyARoutableNormalizedHost(t *testing.T) {
	resolver := &fakeResolver{route: &Route{DeployID: "deploy", Target: "http://target:3000"}}
	handler := NewTLSAskHandler(resolver, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/tls/ask?domain="+url.QueryEscape(" App.Example.Test.:443 "), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(resolver.asked) != 1 || resolver.asked[0] != "app.example.test" {
		t.Fatalf("asked = %#v, want normalized host", resolver.asked)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
}

func TestTLSAskFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "unknown", err: ErrRouteNotFound, want: http.StatusForbidden},
		{name: "store error", err: errors.New("database unavailable"), want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewTLSAskHandler(&fakeResolver{err: tc.err}, zap.NewNop())
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tls/ask?domain=app.example.test", nil))
			if w.Code != tc.want || w.Body.Len() != 0 {
				t.Fatalf("response = %d %q, want %d with empty body", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

func TestTLSAskExposesOnlyTheAskPath(t *testing.T) {
	resolver := &fakeResolver{route: &Route{DeployID: "deploy", Target: "http://target:3000"}}
	handler := NewTLSAskHandler(resolver, zap.NewNop())

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?domain=app.example.test", nil))
	if w.Code != http.StatusNotFound || len(resolver.asked) != 0 {
		t.Fatalf("response = %d, resolver calls = %d; want 404 without a lookup", w.Code, len(resolver.asked))
	}
}

func TestProxyResolvesHostAndPreservesThePublicRequest(t *testing.T) {
	seen := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "served")
	}))
	defer upstream.Close()

	handler := NewProxyHandler(&fakeResolver{route: &Route{DeployID: "deploy", Target: upstream.URL}}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/path?q=one", strings.NewReader("body"))
	req.Host = "App.Example.Test.:443"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated || w.Body.String() != "served" || w.Header().Get("X-Upstream") != "yes" {
		t.Fatalf("response = %d %q headers=%v", w.Code, w.Body.String(), w.Header())
	}
	got := <-seen
	if got.Host != "app.example.test" || got.URL.Path != "/path" || got.URL.RawQuery != "q=one" {
		t.Fatalf("upstream request host=%q uri=%q", got.Host, got.URL.RequestURI())
	}
}

func TestProxyReturnsStableFailureStatuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route *Route
		err   error
		want  int
	}{
		{name: "unknown", err: ErrRouteNotFound, want: http.StatusNotFound},
		{name: "store error", err: errors.New("database unavailable"), want: http.StatusServiceUnavailable},
		{name: "bad target", route: &Route{DeployID: "deploy", Target: "file:///etc/passwd"}, want: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewProxyHandler(&fakeResolver{route: tc.route, err: tc.err}, zap.NewNop())
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "app.example.test"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
