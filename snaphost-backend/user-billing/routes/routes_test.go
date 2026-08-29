package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/user-billing/internal/account"
	"snaphost/user-billing/internal/apikey"
)

const testSecret = "test-webhook-secret"

// newEngine registers the real route table. The handlers are wired over a nil
// pool: these tests assert routing and authorisation, and every case here is
// refused before a handler would touch the database.
func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := zap.NewNop()
	accountHandler := account.NewHandler(account.NewRepository(nil), log)
	apikeyHandler := apikey.NewHandler(apikey.NewRepository(nil), log)

	r := gin.New()
	Register(r, accountHandler, nil, apikeyHandler, nil, nil, nil, testSecret)
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
// /internal/users because it is the surviving internal endpoint, but the
// middleware is registered on the group, so this covers every route under it.
func TestInternalGroupRequiresTheWebhookSecret(t *testing.T) {
	body := `{"id":"9a5b3f1e-0000-4000-8000-000000000000","email":"someone@example.com"}`

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
			w := request(t, r, http.MethodPost, "/internal/users", body, tc.headers)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("answered %d, want 401 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

// Recording an account must not be reachable without the secret, and must not
// be reachable from the public surface at all: the email it carries is the only
// identity this database ever receives.
func TestAccountCreateIsNotPublic(t *testing.T) {
	r := newEngine(t)

	w := request(t, r, http.MethodPost, "/api/v1/users",
		`{"id":"9a5b3f1e-0000-4000-8000-000000000000"}`, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("public /api/v1/users answered %d; it must not be routed (body %s)", w.Code, w.Body.String())
	}
}

// A malformed body is rejected before the handler reaches its nil pool. If this
// ever panics instead of answering 400, the validation moved behind the
// database call.
func TestAccountCreateRejectsAMalformedBody(t *testing.T) {
	auth := map[string]string{"X-Webhook-Secret": testSecret}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"no id", `{"email":"someone@example.com"}`},
		{"id is not a uuid", `{"id":"not-a-uuid","email":"someone@example.com"}`},
		{"not json", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newEngine(t)
			w := request(t, r, http.MethodPost, "/internal/users", tc.body, auth)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("answered %d, want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}
