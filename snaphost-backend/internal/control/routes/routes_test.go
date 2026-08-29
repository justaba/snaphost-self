package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/internal/control/apikey"
	"snaphost/internal/control/auth"
)

const testSecret = "test-webhook-secret"

// newEngine registers the real route table. The handlers are wired over a nil
// pool: these tests assert routing and authorisation, and every case here is
// refused before a handler would touch the database.
func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := zap.NewNop()
	apikeyHandler := apikey.NewHandler(apikey.NewRepository(nil), log)
	authHandler := auth.NewHandler(auth.NewService(auth.NewRepository(nil), 0), auth.CookieOptions{}, log)

	r := gin.New()
	// Both halves, because the assertions below span them: that no billing
	// path is routable anywhere, and that the internal group refuses a wrong
	// secret. Registering only one would make the first pass vacuously.
	Register(r, authHandler, nil, apikeyHandler, nil, nil)
	RegisterInternal(r, nil, apikeyHandler, nil, testSecret)
	return r
}

func request(t *testing.T, r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// This platform has no billing, and adding one back is not a small change: the
// upstream project shipped a public topup endpoint that minted credit from a
// request body, and it stayed that way for months.
//
// So the assertion is that no billing surface exists at all, rather than that
// a particular one is authenticated. If billing ever returns it should fail
// here first and be designed deliberately, not arrive by a copied route line.
func TestNoBillingSurfaceExists(t *testing.T) {
	r := newEngine(t)

	for _, path := range []string{
		"/api/v1/billing",
		"/api/v1/billing/topup",
		"/internal/billing/topup",
		"/internal/billing/reserve",
		"/internal/billing/commit",
		"/internal/billing/refund",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			w := request(t, r, method, path, `{}`, map[string]string{"X-Webhook-Secret": testSecret})
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s answered %d; no billing route may be registered (body %s)",
					method, path, w.Code, w.Body.String())
			}
		}
	}
}

// The internal group is reachable only with the shared secret. Checked on
// /internal/keys/verify because the middleware is registered on the group, so
// one route covers every route under it.
func TestInternalGroupRequiresTheWebhookSecret(t *testing.T) {
	body := `{"key":"sk_0000000000000000"}`

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"no header at all", nil},
		{"empty value", map[string]string{"X-Webhook-Secret": ""}},
		{"wrong secret", map[string]string{"X-Webhook-Secret": "not-the-secret"}},
		{"secret with trailing space", map[string]string{"X-Webhook-Secret": testSecret + " "}},
		{"prefix of the secret", map[string]string{"X-Webhook-Secret": testSecret[:len(testSecret)-1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newEngine(t)
			w := request(t, r, http.MethodPost, "/internal/keys/verify", body, tc.headers)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("answered %d, want 401 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

// Supabase's signup webhook and the endpoint it called are both gone, and
// nothing may have re-created a way to add an account over HTTP. An account is
// created by the operator bootstrap, with a password; POST /internal/users
// could never set one, which is why it had no reason to survive.
func TestNoAccountCreationEndpointExists(t *testing.T) {
	r := newEngine(t)
	secret := map[string]string{"X-Webhook-Secret": testSecret}
	body := `{"id":"9a5b3f1e-0000-4000-8000-000000000000","email":"someone@example.com"}`

	for _, path := range []string{
		"/internal/users",
		"/api/v1/users",
		"/internal/webhooks/supabase",
	} {
		w := request(t, r, http.MethodPost, path, body, secret)
		if w.Code != http.StatusNotFound {
			t.Errorf("POST %s answered %d; it must not be routed (body %s)", path, w.Code, w.Body.String())
		}
	}
}

// The session routes have to be registered under the paths the RBAC policy and
// middleware.PublicRoutes name. A route registered at a different path fails
// authorisation for every role, which looks like a permissions bug rather than
// a typo.
func TestSessionRoutesAreRegisteredAtTheExpectedPaths(t *testing.T) {
	r := newEngine(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/password"},
	} {
		// No body and no identity headers, so each handler refuses — but a 404
		// would mean the route is not there at all.
		w := request(t, r, tc.method, tc.path, `{}`, nil)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s is not routed", tc.method, tc.path)
		}
	}
}

// Me and ChangePassword read the identity the middleware established. Without
// it they must refuse rather than act on a header a client supplied — Enrich
// deletes those headers on an unauthenticated request, and these handlers are
// the far side of that contract.
func TestSessionRoutesRefuseWithoutAnIdentity(t *testing.T) {
	r := newEngine(t)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/auth/me", ""},
		{http.MethodPost, "/api/v1/auth/password", `{"current_password":"a","new_password":"b"}`},
	} {
		w := request(t, r, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d, want 401 (body %s)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
