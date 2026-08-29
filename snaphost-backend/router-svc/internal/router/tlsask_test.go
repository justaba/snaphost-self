package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// billingStub stands in for user-billing's /internal/tls/authorize.
func billingStub(t *testing.T, status int, record *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Webhook-Secret") != "s3cret" {
			t.Errorf("billing called without the shared secret")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if record != nil {
			*record = append(*record, r.URL.Query().Get("domain"))
		}
		w.WriteHeader(status)
	}))
}

func askTLS(t *testing.T, h http.Handler, host string) int {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/tls/authorize?domain="+url.QueryEscape(host), nil))
	return w.Code
}

func newAskHandler(billingURL, suffix string) *TLSAskHandler {
	return &TLSAskHandler{
		Client: &LookupClient{BaseURL: billingURL, Secret: "s3cret"},
		Suffix: suffix,
	}
}

func TestTLSAskAllowsVerifiedDomain(t *testing.T) {
	var asked []string
	srv := billingStub(t, http.StatusOK, &asked)
	defer srv.Close()

	if got := askTLS(t, newAskHandler(srv.URL, "snaphost.pw"), "app.example.com"); got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if len(asked) != 1 || asked[0] != "app.example.com" {
		t.Fatalf("billing asked for %v, want [app.example.com]", asked)
	}
}

func TestTLSAskRefusesWhatBillingRefuses(t *testing.T) {
	srv := billingStub(t, http.StatusForbidden, nil)
	defer srv.Close()

	if got := askTLS(t, newAskHandler(srv.URL, "snaphost.pw"), "unknown.example.org"); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Our own suffix is served by the Yandex gateway under a wildcard certificate.
// Issuing a second one here would be a wasted ACME request and would mean this
// edge started answering for traffic that is not its to serve.
func TestTLSAskRefusesOurOwnSuffixWithoutAskingBilling(t *testing.T) {
	var asked []string
	srv := billingStub(t, http.StatusOK, &asked)
	defer srv.Close()
	h := newAskHandler(srv.URL, "snaphost.pw")

	for _, host := range []string{"snaphost.pw", "proj-abc.snaphost.pw", "PROJ-ABC.SNAPHOST.PW."} {
		if got := askTLS(t, h, host); got != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", host, got)
		}
	}
	if len(asked) != 0 {
		t.Errorf("billing was consulted for our own suffix: %v", asked)
	}
}

// An unreachable control plane must not open the gate.
func TestTLSAskFailsClosedWhenBillingIsDown(t *testing.T) {
	srv := billingStub(t, http.StatusOK, nil)
	srv.Close() // refuse connections

	if got := askTLS(t, newAskHandler(srv.URL, "snaphost.pw"), "app.example.com"); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

// A 500 from billing means "we do not know", which must not become "yes".
func TestTLSAskFailsClosedOnUnexpectedStatus(t *testing.T) {
	srv := billingStub(t, http.StatusInternalServerError, nil)
	defer srv.Close()

	if got := askTLS(t, newAskHandler(srv.URL, "snaphost.pw"), "app.example.com"); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

func TestTLSAskRejectsEmptyAndNonGET(t *testing.T) {
	srv := billingStub(t, http.StatusOK, nil)
	defer srv.Close()
	h := newAskHandler(srv.URL, "snaphost.pw")

	if got := askTLS(t, h, ""); got != http.StatusBadRequest {
		t.Errorf("empty domain: status = %d, want 400", got)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/internal/tls/authorize?domain=app.example.com", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", w.Code)
	}
}

// Caddy sends the SNI name as-is; billing must be asked about the normalized
// form so a trailing dot cannot present a verified name as a new one.
func TestTLSAskNormalizesBeforeAsking(t *testing.T) {
	var asked []string
	srv := billingStub(t, http.StatusOK, &asked)
	defer srv.Close()
	h := newAskHandler(srv.URL, "snaphost.pw")

	for _, raw := range []string{"App.Example.COM", "app.example.com.", "app.example.com:443"} {
		if got := askTLS(t, h, raw); got != http.StatusOK {
			t.Errorf("%q: status = %d, want 200", raw, got)
		}
	}
	for _, a := range asked {
		if a != "app.example.com" {
			t.Errorf("billing asked for %q, want the normalized host", a)
		}
	}
}
