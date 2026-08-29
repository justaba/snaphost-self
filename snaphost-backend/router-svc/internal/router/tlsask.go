package router

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthorizeTLS asks user-billing whether a hostname is a verified custom
// domain, i.e. whether a certificate may be issued for it.
func (c *LookupClient) AuthorizeTLS(ctx context.Context, host string) (bool, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	u := base + "/internal/tls/authorize?domain=" + url.QueryEscape(host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Webhook-Secret", c.Secret)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("authorize tls: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusForbidden, http.StatusBadRequest:
		return false, nil
	default:
		// Anything else is "we do not know", which must not become "yes".
		return false, fmt.Errorf("authorize tls: unexpected status %d", resp.StatusCode)
	}
}

// TLSAskHandler serves Caddy's on-demand TLS `ask` probe.
//
// Caddy's `ask` is a bare URL with no way to attach a header, so this endpoint
// cannot itself require the shared secret. That is exactly why it lives here
// rather than on user-billing: router-svc is published on loopback only, so
// the unauthenticated surface never leaves the host, and the secret-bearing
// call to user-billing happens on this side of it.
//
// It must not be registered on the Yandex deployment of this same image, which
// sits behind a public API Gateway. `TLS_ASK_ENABLED` gates it, and defaults to
// off.
type TLSAskHandler struct {
	Client *LookupClient
	// Suffix is our own generated-hostname domain. Hosts under it are served
	// by the Yandex gateway with its wildcard certificate and must never
	// obtain a second certificate here.
	Suffix string
}

// tlsAskTimeout bounds the control-plane call. Caddy holds the TLS handshake
// open while it waits, so a slow answer is a stalled connection for the
// visitor; failing closed quickly is better than hanging.
const tlsAskTimeout = 5 * time.Second

func (h *TLSAskHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	host, err := NormalizeHost(r.URL.Query().Get("domain"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Refuse our own suffix outright. A generated hostname already has a
	// wildcard certificate from the Yandex path; issuing another here would be
	// a pointless ACME request and would mean this edge started answering for
	// traffic that is not its to serve.
	if h.Suffix != "" {
		if suffix, err := NormalizeHost(h.Suffix); err == nil {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), tlsAskTimeout)
	defer cancel()

	allowed, err := h.Client.AuthorizeTLS(ctx, host)
	if err != nil {
		// Fail closed: an unreachable control plane must not open the gate.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}
