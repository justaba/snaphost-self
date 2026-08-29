package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/api-gateway/config"
)

const testWebhookSecret = "router-internal-secret"

func newIngressEngine(target string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRouterIngress(r, &config.Config{
		WebhookSecret: testWebhookSecret,
		Services: config.Services{
			UserBilling: target,
		},
	}, zap.NewNop())
	return r
}

func performRequest(r http.Handler, method, path, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if secret != "" {
		req.Header.Set("X-Webhook-Secret", secret)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(closeNotifyRecorder{ResponseRecorder: w}, req)
	return w
}

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func TestRouterIngressRejectsMissingAndWrongSecret(t *testing.T) {
	r := newIngressEngine("http://127.0.0.1:1")
	for _, tc := range []struct {
		name   string
		secret string
	}{
		{name: "missing"},
		{name: "wrong", secret: "wrong-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := performRequest(r, http.MethodGet, "/internal/routes?host=app.example.com", tc.secret)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if (tc.secret != "" && strings.Contains(w.Body.String(), tc.secret)) || strings.Contains(w.Body.String(), testWebhookSecret) {
				t.Fatal("response body contains a webhook secret")
			}
		})
	}
}

func TestRouterIngressProxiesExactRouteAndPreservesResponse(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotSecret, gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotSecret = r.Header.Get("X-Webhook-Secret")
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"route":"upstream"}`)
	}))
	defer upstream.Close()

	r := newIngressEngine(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/internal/routes?host=App.Example.com%3A443", nil)
	req.Header.Set("X-Webhook-Secret", testWebhookSecret)
	req.Header.Set("Authorization", "") // User authorization is not required.
	w := httptest.NewRecorder()
	r.ServeHTTP(closeNotifyRecorder{ResponseRecorder: w}, req)

	if gotMethod != http.MethodGet || gotPath != "/internal/routes" {
		t.Fatalf("upstream request = %s %s", gotMethod, gotPath)
	}
	if gotQuery != "host=App.Example.com%3A443" {
		t.Fatalf("upstream query = %q", gotQuery)
	}
	if gotSecret != testWebhookSecret {
		t.Fatal("webhook secret was not forwarded to billing")
	}
	if gotAuthorization != "" {
		t.Fatalf("unexpected Authorization header = %q", gotAuthorization)
	}
	if w.Code != http.StatusTeapot || w.Body.String() != `{"route":"upstream"}` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
}

func TestRouterIngressUnavailableBillingReturnsBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := upstream.URL
	upstream.Close()

	w := performRequest(newIngressEngine(target), http.MethodGet, "/internal/routes?host=app.example.com", testWebhookSecret)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
	if strings.Contains(w.Body.String(), testWebhookSecret) {
		t.Fatal("response body contains webhook secret")
	}
}

func TestRouterIngressDoesNotExposeOtherMethodsOrInternalRoutes(t *testing.T) {
	r := newIngressEngine("http://127.0.0.1:1")
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/internal/routes"},
		{method: http.MethodGet, path: "/internal/deploys"},
	} {
		w := performRequest(r, tc.method, tc.path, testWebhookSecret)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, w.Code, http.StatusNotFound)
		}
	}
}
