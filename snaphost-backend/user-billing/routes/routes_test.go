package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/user-billing/internal/apikey"
	"snaphost/user-billing/internal/wallet"
)

const testSecret = "test-webhook-secret"

// newEngine registers the real route table. The handlers are wired over a nil
// pool: these tests assert routing and authorisation, and every case here is
// refused before a handler would touch the database.
func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := zap.NewNop()
	walletHandler := wallet.NewHandler(wallet.NewService(wallet.NewRepository(nil), log), log, testSecret, 100)
	apikeyHandler := apikey.NewHandler(apikey.NewRepository(nil), log)

	r := gin.New()
	Register(r, walletHandler, nil, apikeyHandler, nil, nil, nil, testSecret)
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

// Until 2026-08-07 this route was public and took the credited amount from the
// request body of whoever was logged in — free coins for any account or any
// `sk_` key. Nothing may put it back on the public surface: real top-ups have
// to arrive through a verified payment callback on the internal endpoint.
func TestPublicTopupRouteDoesNotExist(t *testing.T) {
	r := newEngine(t)

	w := request(t, r, http.MethodPost, "/api/v1/billing/topup",
		`{"amount":1000000,"idempotency_key":"free-money"}`,
		map[string]string{"X-User-ID": "11111111-2222-3333-4444-555555555555"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("public topup answered %d; it must not be routed at all (body %s)", w.Code, w.Body.String())
	}
}

func TestInternalTopupRequiresTheWebhookSecret(t *testing.T) {
	r := newEngine(t)
	body := `{"user_id":"11111111-2222-3333-4444-555555555555","amount":100,"idempotency_key":"k"}`

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"no secret", nil},
		{"wrong secret", map[string]string{"X-Webhook-Secret": "not-the-secret"}},
		{"empty secret", map[string]string{"X-Webhook-Secret": ""}},
		{"user header instead", map[string]string{"X-User-ID": "11111111-2222-3333-4444-555555555555"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, r, http.MethodPost, "/internal/billing/topup", body, tc.headers)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("got %d want 401 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

// With the secret accepted, the handler must still refuse a body that does not
// name a user — the caller credits somebody else's wallet, so the account can
// never be implied.
func TestInternalTopupRejectsAMissingUser(t *testing.T) {
	r := newEngine(t)
	auth := map[string]string{"X-Webhook-Secret": testSecret}

	cases := []struct {
		name string
		body string
	}{
		{"no user_id", `{"amount":100,"idempotency_key":"k"}`},
		{"malformed user_id", `{"user_id":"not-a-uuid","amount":100,"idempotency_key":"k"}`},
		{"no idempotency key", `{"user_id":"11111111-2222-3333-4444-555555555555","amount":100}`},
		{"zero amount", `{"user_id":"11111111-2222-3333-4444-555555555555","amount":0,"idempotency_key":"k"}`},
		{"negative amount", `{"user_id":"11111111-2222-3333-4444-555555555555","amount":-500,"idempotency_key":"k"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, r, http.MethodPost, "/internal/billing/topup", tc.body, auth)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("got %d want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

// The wallet read stays public — it is the user's own balance.
func TestBillingReadStaysPublic(t *testing.T) {
	r := newEngine(t)

	w := request(t, r, http.MethodGet, "/api/v1/billing", "", nil)
	if w.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/billing is no longer routed")
	}
}
