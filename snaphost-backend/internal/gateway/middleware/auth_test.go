package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"snaphost/internal/shared"
)

type fakeSessions struct {
	identity Identity
	err      error
	seen     string
}

func (f *fakeSessions) Verify(_ context.Context, token string) (Identity, error) {
	f.seen = token
	if f.err != nil {
		return Identity{}, f.err
	}
	return f.identity, nil
}

type fakeKeys struct {
	userID string
	err    error
}

func (f *fakeKeys) Verify(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.userID, nil
}

// newAuthEngine wires Auth in front of a handler that reports what the
// middleware put on the context, so the assertions are about the identity that
// reached the handler rather than about a status code alone.
func newAuthEngine(sessions SessionVerifier, keys KeyVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Auth(sessions, keys))

	report := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id": c.GetString(string(CtxKeyUserID)),
			"email":   c.GetString(string(CtxKeyUserEmail)),
			"role":    c.GetString(string(CtxKeyUserRole)),
		})
	}
	r.POST("/api/v1/auth/login", report)
	r.GET("/api/v1/deploys", report)
	return r
}

func do(r *gin.Engine, method, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLoginIsTheOnlyPublicAPIRoute(t *testing.T) {
	for key := range PublicRoutes {
		switch key {
		case "POST:/api/v1/auth/login", "GET:/health", "GET:/metrics":
		default:
			t.Errorf("PublicRoutes carries %q; every other route must authenticate", key)
		}
	}

	r := newAuthEngine(&fakeSessions{err: errors.New("no session")}, nil)
	if w := do(r, http.MethodPost, "/api/v1/auth/login", nil); w.Code != http.StatusOK {
		t.Fatalf("login answered %d without a credential, want 200", w.Code)
	}
}

func TestSessionCookieEstablishesTheIdentity(t *testing.T) {
	sessions := &fakeSessions{identity: Identity{UserID: "user-1", Email: "op@example.test", Role: "admin"}}
	r := newAuthEngine(sessions, nil)

	w := do(r, http.MethodGet, "/api/v1/deploys", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: shared.SessionCookie, Value: "the-token"})
	})
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if sessions.seen != "the-token" {
		t.Errorf("verified %q, want the cookie value", sessions.seen)
	}
	if body := w.Body.String(); !strings.Contains(body, "user-1") || !strings.Contains(body, "admin") {
		t.Errorf("handler saw %s, want the verified identity", body)
	}
}

func TestMissingAndInvalidSessionsAreRefused(t *testing.T) {
	r := newAuthEngine(&fakeSessions{err: errors.New("no session")}, nil)

	if w := do(r, http.MethodGet, "/api/v1/deploys", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no cookie answered %d, want 401", w.Code)
	}
	w := do(r, http.MethodGet, "/api/v1/deploys", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: shared.SessionCookie, Value: "stale"})
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a revoked session answered %d, want 401", w.Code)
	}
}

// An sk_ key is how every non-browser client authenticates, and it must keep
// working exactly as it did — this is the path the MCP server and the editor
// extension are on.
func TestAPIKeyAuthenticatesAsAnOrdinaryUser(t *testing.T) {
	r := newAuthEngine(&fakeSessions{err: errors.New("no session")}, &fakeKeys{userID: "key-owner"})

	w := do(r, http.MethodGet, "/api/v1/deploys", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer sk_"+"0123456789abcdef")
	})
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "key-owner") || !strings.Contains(body, `"role":"user"`) {
		t.Errorf("handler saw %s, want the key's owner as an ordinary user", body)
	}
}

// A wrong Authorization header must not fall through to the cookie. Otherwise a
// client holding a revoked key would act as whoever last logged in on that
// browser, which is a privilege escalation rather than a fallback.
func TestABadAuthorizationHeaderDoesNotFallBackToTheCookie(t *testing.T) {
	sessions := &fakeSessions{identity: Identity{UserID: "user-1", Role: "admin"}}
	r := newAuthEngine(sessions, &fakeKeys{err: errors.New("revoked")})

	for _, header := range []string{
		"Bearer sk_revoked",
		"Bearer not-an-api-key",
		"Basic dXNlcjpwYXNz",
		"sk_no_scheme",
	} {
		w := do(r, http.MethodGet, "/api/v1/deploys", func(req *http.Request) {
			req.Header.Set("Authorization", header)
			req.AddCookie(&http.Cookie{Name: shared.SessionCookie, Value: "a-valid-session"})
		})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q answered %d, want 401 (body %s)", header, w.Code, w.Body.String())
		}
	}
}
