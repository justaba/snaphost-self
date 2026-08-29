// Package clone provides Git repository cloning with URL validation, workdir
// isolation, and security checks (SSRF prevention, symlink escape detection).
package clone

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Typed sentinel errors returned by ValidateRepoURL / ValidateRepoURLSyntactic.
// Callers (currently internal/builder/pipeline.executePipeline) use
// errors.Is to classify failures as transient vs permanent — see
// pipeline/errors.go.
//
//	ErrInvalidURL       — syntactic problem with the URL itself (scheme,
//	                      length, hostname, path shape). Permanent.
//	ErrHostNotAllowed   — hostname not in the configured allowlist. Permanent.
//	ErrResolverFailure  — DNS resolution failed (resolver down, NXDOMAIN
//	                      that may be transient, network error during
//	                      LookupIP). Transient.
//	ErrIPFiltered       — DNS resolved successfully but every returned IP
//	                      was rejected by the security filter (loopback /
//	                      private / link-local / cloud metadata). Permanent.
var (
	ErrInvalidURL      = errors.New("invalid url")
	ErrHostNotAllowed  = errors.New("host not in allowlist")
	ErrResolverFailure = errors.New("dns resolver failure")
	ErrIPFiltered      = errors.New("resolved ip rejected by filter")
)

// IPResolver is the subset of *net.Resolver required for URL validation.
// Tests substitute a fake resolver to drive the IP filter without real DNS.
type IPResolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// ValidatedURL is the result of a successful repo URL validation. Callers
// that perform the actual clone MUST connect to IP, not Host — pinning the
// validated IP closes the TOCTOU window between validation-time DNS lookup
// and clone-time DNS lookup (defence against DNS-rebinding to cloud
// metadata endpoints, e.g. 169.254.169.254).
type ValidatedURL struct {
	URL  string // original URL as supplied
	Host string // hostname extracted from URL (lower-case)
	IP   net.IP // validated IP that all connections must be pinned to
}

// cloudMetadataIPs are well-known metadata-service addresses across cloud
// providers. They are blocked in addition to the broader link-local / private
// filters so that any future provider using a non-link-local metadata IP is
// still rejected.
var cloudMetadataIPs = []net.IP{
	net.IPv4(169, 254, 169, 254),   // AWS, GCP, Azure, Yandex Cloud, most others
	net.ParseIP("fd00:ec2::254"),   // AWS IPv6
	net.ParseIP("fe80::a9fe:a9fe"), // some providers
}

// isAllowedIP returns true if the IP is publicly routable and not in any
// known-dangerous range (loopback, private, link-local, multicast,
// unspecified, cloud metadata). IPv4-mapped IPv6 addresses are checked
// against the IPv4 view before the IPv6 view so that ::ffff:169.254.169.254
// is rejected the same as 169.254.169.254.
func isAllowedIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Normalise IPv4-mapped IPv6 → IPv4. ip.To4() returns a 4-byte slice
	// for both native IPv4 and ::ffff:IPv4; nil otherwise.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	for _, blocked := range cloudMetadataIPs {
		if blocked == nil {
			continue
		}
		if ip.Equal(blocked) {
			return false
		}
	}
	return true
}

// ValidateRepoURLSyntactic performs the cheap, no-network checks on a repo
// URL — scheme, length, hostname shape, path shape. Used by the API
// handler to reject obviously malformed input before enqueueing. Full
// validation (DNS resolution + IP filter) is repeated by the worker via
// ValidateRepoURL immediately before clone; the TOCTOU window between
// API and worker is closed by the worker re-validating and pinning, not
// by the API trying to validate eagerly.
func ValidateRepoURLSyntactic(rawURL string) error {
	if len(rawURL) > 2000 {
		return fmt.Errorf("%w: url too long (%d bytes)", ErrInvalidURL, len(rawURL))
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if strings.ToLower(parsed.Scheme) != "https" {
		return fmt.Errorf("%w: scheme is %q, expected https", ErrInvalidURL, parsed.Scheme)
	}
	// Credentials belong in the git_token field (14b-3), never in the URL
	// where they would be persisted in the deploy row and logged.
	if parsed.User != nil {
		return fmt.Errorf("%w: credentials embedded in the URL are not allowed", ErrInvalidURL)
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("%w: empty hostname", ErrInvalidURL)
	}
	if len(hostname) > 253 {
		return fmt.Errorf("%w: hostname too long", ErrInvalidURL)
	}
	path := strings.TrimSuffix(parsed.Path, ".git")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("%w: path must be /owner/repo, got %q", ErrInvalidURL, parsed.Path)
	}
	for _, seg := range parts {
		if seg == ".." || seg == "." {
			return fmt.Errorf("%w: path contains traversal segment %q", ErrInvalidURL, seg)
		}
	}
	return nil
}

// ValidateRepoURL checks that the given repository URL is safe to clone.
// It enforces HTTPS-only, allowed-hosts whitelisting, valid path format,
// and SSRF protection by resolving the hostname and rejecting any IP that
// could reach private networks or cloud-metadata endpoints.
//
// On success, returns a *ValidatedURL containing the IP the caller MUST
// pin all subsequent connections to. A nil resolver defaults to
// net.DefaultResolver.
func ValidateRepoURL(ctx context.Context, rawURL string, allowedHosts []string, resolver IPResolver) (*ValidatedURL, error) {
	if len(rawURL) > 2000 {
		return nil, fmt.Errorf("%w: url too long (%d bytes)", ErrInvalidURL, len(rawURL))
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// 1. Scheme must be exactly "https".
	if strings.ToLower(parsed.Scheme) != "https" {
		return nil, fmt.Errorf("%w: scheme is %q, expected https", ErrInvalidURL, parsed.Scheme)
	}

	// Credentials belong in the git_token field (14b-3), never in the URL.
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: credentials embedded in the URL are not allowed", ErrInvalidURL)
	}

	// 2. Hostname must be in the allowedHosts list (case-insensitive).
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return nil, fmt.Errorf("%w: empty hostname", ErrInvalidURL)
	}
	if len(hostname) > 253 {
		return nil, fmt.Errorf("%w: hostname too long", ErrInvalidURL)
	}
	allowed := false
	for _, h := range allowedHosts {
		if strings.ToLower(h) == hostname {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: %q", ErrHostNotAllowed, hostname)
	}

	// 3. Path must have form /owner/repo with non-empty segments.
	path := strings.TrimSuffix(parsed.Path, ".git")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("%w: path must be /owner/repo, got %q", ErrInvalidURL, parsed.Path)
	}

	// 4. DNS resolution + IP filter.
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		return nil, fmt.Errorf("%w: lookup %q: %v", ErrResolverFailure, hostname, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: dns returned no ips for %q", ErrResolverFailure, hostname)
	}

	// Pick the first allowed IP, preferring IPv4 (universal metadata-IP
	// coverage; IPv6 metadata varies by provider so IPv4 is the safer pin).
	var validated net.IP
	for _, ip := range ips {
		if ip.To4() != nil && isAllowedIP(ip) {
			validated = ip
			break
		}
	}
	if validated == nil {
		for _, ip := range ips {
			if isAllowedIP(ip) {
				validated = ip
				break
			}
		}
	}
	if validated == nil {
		return nil, fmt.Errorf("%w: %q resolved to no allowed ips", ErrIPFiltered, hostname)
	}

	return &ValidatedURL{
		URL:  rawURL,
		Host: hostname,
		IP:   validated,
	}, nil
}
