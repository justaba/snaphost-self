package domain

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// VerifiedHostStore is the single question the TLS edge asks. It is a separate
// interface from Store because the caller is different in kind: Store serves
// requests made by a signed-in user about their own domains, this serves the
// edge, which has no user.
type VerifiedHostStore interface {
	IsVerifiedHost(ctx context.Context, host string) (bool, error)
}

// TLSHandler answers Caddy's on-demand TLS `ask` probe.
type TLSHandler struct {
	store VerifiedHostStore
	log   *zap.Logger
}

func NewTLSHandler(store VerifiedHostStore, log *zap.Logger) *TLSHandler {
	return &TLSHandler{store: store, log: log}
}

// Authorize handles GET /internal/tls/authorize?domain=<host>.
//
// Caddy calls this before obtaining a certificate for a hostname it has never
// seen. Answering `200` authorises an ACME issuance; anything else refuses it.
//
// This gate is the reason on-demand TLS is safe to run at all. Without it, any
// hostname pointed at our edge by anyone would trigger issuance — burning ACME
// rate limits that are shared across everything we serve, and letting a
// stranger make us request certificates for domains we have no relationship
// with. Only a row in `custom_domains` with `status = 'verified'` counts, which
// means the attach flow and its TXT challenge have already proven the person
// asking controls that name.
//
// The response body is deliberately empty: Caddy reads only the status code,
// and an error message here would describe our internal state to whoever
// pointed DNS at us.
func (h *TLSHandler) Authorize(c *gin.Context) {
	host := Normalize(c.Query("domain"))
	if host == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	verified, err := h.store.IsVerifiedHost(c.Request.Context(), host)
	if err != nil {
		// Fail closed. A database blip must not become an open issuance gate;
		// Caddy retries, and a certificate arriving a minute late is a far
		// smaller problem than one issued for a name nobody proved.
		h.log.Error("tls authorize lookup failed", zap.String("host", host), zap.Error(err))
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if !verified {
		// Logged at info: this fires for every stray hostname aimed at the
		// edge, which is normal background noise, but a burst of it is the
		// signal that someone is probing us.
		h.log.Info("tls issuance refused for unverified host", zap.String("host", host))
		c.Status(http.StatusForbidden)
		return
	}
	c.Status(http.StatusOK)
}
