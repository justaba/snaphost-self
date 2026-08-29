package router

import (
	"net/http"
	"strings"
)

// confineCookiesToHost strips the Domain attribute from every Set-Cookie a
// deploy returns, so its cookies apply to that one hostname and nothing else.
//
// Generated deploy hostnames all sit under one registrable domain. Browsers
// scope cookies per registrable domain, so without this a deploy could answer
// with `Set-Cookie: sid=…; Domain=<suffix>` and every other user's deploy would
// receive it — cookie tossing between tenants who have nothing to do with each
// other. Removing the attribute makes the cookie host-only, which is what a
// single-hostname application needs anyway.
//
// This is a stopgap, not the fix. The fix is a Public Suffix List entry for the
// deploy suffix, which makes the browser itself treat each deploy hostname as a
// separate site; the PSL takes months and may be refused. Two gaps remain until
// then, and both are why the PSL entry still matters:
//
//   - cookies written by page JavaScript (`document.cookie = "…; domain=…"`)
//     never reach this proxy and cannot be touched here;
//   - the local Docker path routes through Traefik straight to the container,
//     bypassing this code entirely.
//
// Custom domains are deliberately left alone: those hostnames belong to the
// customer, the whole registrable domain is theirs, and a cookie shared with
// their own `www` is legitimate. There is no other tenant to protect there.
func confineCookiesToHost(header http.Header, host, domainSuffix string) {
	if !isPlatformSubdomain(host, domainSuffix) {
		return
	}
	cookies := header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}

	confined := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		confined = append(confined, stripCookieDomain(cookie))
	}
	header.Del("Set-Cookie")
	for _, cookie := range confined {
		header.Add("Set-Cookie", cookie)
	}
}

// isPlatformSubdomain reports whether host is one of the hostnames we hand out,
// as opposed to a customer's own domain. An empty suffix protects nothing,
// which keeps a misconfigured router from silently rewriting every response.
func isPlatformSubdomain(host, domainSuffix string) bool {
	suffix, err := NormalizeHost(domainSuffix)
	if err != nil || suffix == "" {
		return false
	}
	normalized, err := NormalizeHost(host)
	if err != nil {
		return false
	}
	return strings.HasSuffix(normalized, "."+suffix)
}

// stripCookieDomain removes the Domain attribute from one Set-Cookie value and
// leaves every other attribute untouched, including the cookie's own value.
// Attribute names are case-insensitive per RFC 6265, and unknown attributes are
// preserved rather than dropped — this function's only job is the domain scope.
func stripCookieDomain(cookie string) string {
	parts := strings.Split(cookie, ";")
	kept := parts[:0:0]
	for i, part := range parts {
		// The first segment is name=value, never an attribute.
		if i > 0 && isDomainAttribute(part) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ";")
}

func isDomainAttribute(part string) bool {
	name := strings.TrimSpace(part)
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	return strings.EqualFold(strings.TrimSpace(name), "domain")
}
