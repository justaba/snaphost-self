package edge

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"go.uber.org/zap"
)

// TLSAskHandler is the whole unauthenticated Caddy authorization surface. A
// 2xx permits certificate issuance; every other result fails closed and the
// empty body reveals no domain state.
type TLSAskHandler struct {
	resolver Resolver
	log      *zap.Logger
}

func NewTLSAskHandler(resolver Resolver, log *zap.Logger) *TLSAskHandler {
	return &TLSAskHandler{resolver: resolver, log: log}
}

func (h *TLSAskHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tls/ask" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	host := NormalizeHost(r.URL.Query().Get("domain"))
	if host == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_, err := h.resolver.Resolve(r.Context(), host)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, ErrRouteNotFound):
		w.WriteHeader(http.StatusForbidden)
	default:
		h.log.Error("tls ask route lookup failed", zap.String("host", host), zap.Error(err))
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// ProxyHandler resolves every request independently, so an atomic alias move
// takes effect on the next request without a Caddy reload or a stale route
// cache. Caddy is the only intended caller; production exposes this listener
// only on the Compose control network.
type ProxyHandler struct {
	resolver Resolver
	log      *zap.Logger
}

func NewProxyHandler(resolver Resolver, log *zap.Logger) *ProxyHandler {
	return &ProxyHandler{resolver: resolver, log: log}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := NormalizeHost(r.Host)
	if host == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	route, err := h.resolver.Resolve(r.Context(), host)
	if err != nil {
		if errors.Is(err, ErrRouteNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.log.Error("edge route lookup failed", zap.String("host", host), zap.Error(err))
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	target, err := url.Parse(route.Target)
	if err != nil || target.Scheme != "http" || target.Host == "" {
		h.log.Error("edge route has invalid target",
			zap.String("deploy_id", route.DeployID), zap.String("target", route.Target), zap.Error(err))
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		// The application may use the public host for redirects or tenant
		// selection. Never replace it with the internal container name.
		req.Host = host
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
		h.log.Warn("edge upstream failed",
			zap.String("host", host), zap.String("deploy_id", route.DeployID), zap.Error(proxyErr))
		w.WriteHeader(http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
