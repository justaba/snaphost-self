package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var ErrRouteNotFound = errors.New("route not found")

type RouteLookup interface {
	Lookup(ctx context.Context, host string) (*Route, error)
}

type ContainerResolver interface {
	ResolveURL(ctx context.Context, containerID string) (string, error)
}

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type Proxy struct {
	DomainSuffix string
	Lookup       RouteLookup
	Resolver     ContainerResolver
	Tokens       TokenSource
	Client       *http.Client
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, err := ResolvableHost(r.Host, p.DomainSuffix)
	if err != nil {
		if errors.Is(err, ErrRouteNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}

	route, err := p.Lookup.Lookup(r.Context(), host)
	if err != nil {
		if errors.Is(err, ErrRouteNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "route lookup failed", http.StatusBadGateway)
		return
	}

	baseURL, err := p.Resolver.ResolveURL(r.Context(), route.ContainerID)
	if err != nil {
		http.Error(w, "container lookup failed", http.StatusBadGateway)
		return
	}
	token, err := p.Tokens.Token(r.Context())
	if err != nil {
		http.Error(w, "container auth failed", http.StatusBadGateway)
		return
	}

	upstreamURL, err := buildUpstreamURL(baseURL, r.URL)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "bad upstream request", http.StatusBadGateway)
		return
	}
	copyHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Host", host)
	req.Header.Set("X-Snaphost-Deploy-ID", route.DeployID)
	req.Header.Set("X-Snaphost-Container-ID", route.ContainerID)

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	confineCookiesToHost(w.Header(), host, p.DomainSuffix)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func NormalizeHost(host string) (string, error) {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	return host, nil
}

// ResolvableHost normalizes an incoming Host and decides whether it is worth
// a route lookup at all. A host under our own suffix must still be a single
// label deep — that namespace is ours and its shape is known. A host outside
// it is a custom domain: we cannot judge its shape, only whether user-billing
// has a verified alias for it, so it is passed through as-is and an unknown
// one comes back as a 404 rather than a 400.
func ResolvableHost(host, domainSuffix string) (string, error) {
	normalized, err := NormalizeHost(host)
	if err != nil {
		return "", err
	}
	suffix, err := NormalizeHost(domainSuffix)
	if err != nil {
		return "", fmt.Errorf("domain suffix is required: %w", err)
	}
	if normalized == suffix {
		// The apex itself is not a deploy.
		return "", ErrRouteNotFound
	}
	if strings.HasSuffix(normalized, "."+suffix) {
		if _, _, err := NormalizeHostForDomain(normalized, suffix); err != nil {
			return "", err
		}
	}
	return normalized, nil
}

func NormalizeHostForDomain(host, domainSuffix string) (normalizedHost, subdomain string, err error) {
	host, err = NormalizeHost(host)
	if err != nil {
		return "", "", err
	}
	domainSuffix, err = NormalizeHost(domainSuffix)
	if err != nil {
		return "", "", fmt.Errorf("domain suffix is required: %w", err)
	}
	if host == domainSuffix {
		return "", "", ErrRouteNotFound
	}
	suffix := "." + domainSuffix
	if !strings.HasSuffix(host, suffix) {
		return "", "", ErrRouteNotFound
	}
	subdomain = strings.TrimSuffix(host, suffix)
	if subdomain == "" || strings.Contains(subdomain, ".") {
		return "", "", ErrRouteNotFound
	}
	return host, subdomain, nil
}

func buildUpstreamURL(base string, incoming *url.URL) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = singleJoiningSlash(u.Path, incoming.EscapedPath())
	u.RawQuery = incoming.RawQuery
	return u.String(), nil
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

func copyHeaders(dst, src http.Header) {
	for k, values := range src {
		if isHopByHopHeader(k) || strings.EqualFold(k, "Authorization") {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}
