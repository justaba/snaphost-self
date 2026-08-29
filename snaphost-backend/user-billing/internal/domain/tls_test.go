package domain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeVerifiedStore struct {
	verified map[string]bool
	err      error
	asked    []string
}

func (f *fakeVerifiedStore) IsVerifiedHost(_ context.Context, host string) (bool, error) {
	f.asked = append(f.asked, host)
	if f.err != nil {
		return false, f.err
	}
	return f.verified[host], nil
}

func newTLSTestServer(store VerifiedHostStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/internal/tls/authorize", NewTLSHandler(store, zap.NewNop()).Authorize)
	return r
}

// ask sends a raw, already-encoded query string.
func ask(t *testing.T, r *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/tls/authorize"+query, nil))
	return w
}

// askDomain encodes the host the way Caddy's client would.
func askDomain(t *testing.T, r *gin.Engine, host string) *httptest.ResponseRecorder {
	t.Helper()
	return ask(t, r, "?domain="+url.QueryEscape(host))
}

func TestAuthorizeAllowsVerifiedDomain(t *testing.T) {
	r := newTLSTestServer(&fakeVerifiedStore{verified: map[string]bool{"app.example.com": true}})

	if got := askDomain(t, r, "app.example.com").Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// The whole point of the gate: a hostname nobody proved must not be able to
// make us request a certificate for it.
func TestAuthorizeRefusesUnknownAndUnverifiedDomains(t *testing.T) {
	store := &fakeVerifiedStore{verified: map[string]bool{"app.example.com": true}}
	r := newTLSTestServer(store)

	for _, host := range []string{"unknown.example.org", "pending.example.com", "evil.invalid"} {
		if got := askDomain(t, r, host).Code; got != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", host, got)
		}
	}
}

// A database blip must not become an open issuance gate. Caddy retries, and a
// certificate arriving late is a smaller problem than one issued for a name
// nobody proved.
func TestAuthorizeFailsClosedOnStoreError(t *testing.T) {
	r := newTLSTestServer(&fakeVerifiedStore{err: errors.New("connection refused")})

	if got := askDomain(t, r, "app.example.com").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

func TestAuthorizeRejectsMissingDomain(t *testing.T) {
	r := newTLSTestServer(&fakeVerifiedStore{})

	for _, q := range []string{"", "?domain=", "?domain=%20"} {
		if got := ask(t, r, q).Code; got != http.StatusBadRequest {
			t.Errorf("%q: status = %d, want 400", q, got)
		}
	}
}

// Caddy sends the SNI name as-is. Normalising it the same way the attach path
// does is what keeps "Example.COM" and "example.com." from being treated as
// hostnames nobody verified.
func TestAuthorizeNormalizesTheAskedHost(t *testing.T) {
	store := &fakeVerifiedStore{verified: map[string]bool{"app.example.com": true}}
	r := newTLSTestServer(store)

	for _, raw := range []string{"App.Example.COM", "app.example.com.", "  app.example.com  ", "app.example.com:443"} {
		if got := askDomain(t, r, raw).Code; got != http.StatusOK {
			t.Errorf("%q: status = %d, want 200", raw, got)
		}
	}
	for _, asked := range store.asked {
		if asked != "app.example.com" {
			t.Errorf("store was asked for %q, want the normalized host", asked)
		}
	}
}

// The edge is talking to whoever pointed DNS at us; the response must not
// describe our internal state.
func TestAuthorizeRevealsNothingInTheBody(t *testing.T) {
	r := newTLSTestServer(&fakeVerifiedStore{verified: map[string]bool{"app.example.com": true}})

	for _, q := range []string{"?domain=app.example.com", "?domain=unknown.example.org", "?domain="} {
		if body := ask(t, r, q).Body.String(); body != "" {
			t.Errorf("%q: body = %q, want empty", q, body)
		}
	}
}
