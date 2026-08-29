// Package domain owns custom domains — the alias layer of the
// project/deploy/alias model (Task 16b).
//
// A domain is a pointer to one deploy, held by a project. Publishing moves
// the pointer; rollback moves it back with no rebuild. Nothing routes until
// the user has proven ownership through a TXT record: without that proof any
// user could attach a hostname someone else controls, and on an on-demand-TLS
// edge drive certificate issuance for it.
package domain

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Domain statuses. Only StatusVerified routes traffic.
const (
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusFailed   = "failed"
	StatusRevoked  = "revoked"
)

// ChallengePrefix is the label the verification TXT record lives under, so a
// user never has to put our token on the apex record itself.
const ChallengePrefix = "_snaphost-verify"

// Reason codes stored in custom_domains.last_error. These are stable
// identifiers rather than prose: the dashboard has to explain a stuck domain
// in the user's own language, and matching on English sentences would break
// the moment one is reworded. The detail behind a code goes to the logs.
const (
	// ReasonTXTNotFound — the challenge record does not exist yet. Usually
	// means DNS has not propagated, not that the user did anything wrong.
	ReasonTXTNotFound = "txt_not_found"
	// ReasonTXTMismatch — a record exists but carries a different value.
	ReasonTXTMismatch = "txt_mismatch"
	// ReasonDNSLookupFailed — the resolver failed; this proves nothing and
	// never costs a verified domain its status.
	ReasonDNSLookupFailed = "dns_lookup_failed"
	// ReasonUnpinnedIdle — the alias was released because its target served
	// no traffic for the configured window.
	ReasonUnpinnedIdle = "unpinned_idle"
)

// Domain is one attached hostname.
type Domain struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"user_id"`
	ProjectID         uuid.UUID  `json:"project_id"`
	TargetDeployID    *uuid.UUID `json:"target_deploy_id,omitempty"`
	Domain            string     `json:"domain"`
	VerificationToken string     `json:"verification_token"`
	Status            string     `json:"status"`
	LastError         *string    `json:"last_error,omitempty"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ChallengeRecord is the TXT record name the user must create.
func (d Domain) ChallengeRecord() string { return ChallengePrefix + "." + d.Domain }

// NewToken returns a fresh per-domain verification token. It is stable for
// the life of the row: a user retrying verification should not have to edit
// DNS a second time.
func NewToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate verification token: %w", err)
	}
	return "snaphost-verify=" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Normalize lowercases the hostname and strips the parts a browser would not
// send anyway — surrounding space, a port, a trailing dot. It is deliberately
// the same normalization the router applies to generated hostnames, so both
// route-lookup branches agree on what a host is.
func Normalize(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// Validate checks an attach request. reservedZones are the domains the
// platform owns — the runtime suffix whose subdomains we hand out, and the
// control-plane domain the dashboard and API answer on. Neither may be
// claimed by a user: the first is our namespace to allocate, and the second is
// where sessions live, so an attach that even reached `pending` there would be
// a confusing dead end at best.
//
// The returned code is a stable identifier the dashboard maps to an
// explanation, not just an error string.
func Validate(host string, reservedZones []string) (code, message string) {
	if host == "" {
		return "invalid_domain", "domain is required"
	}
	if len(host) > 253 {
		return "invalid_domain", "domain is longer than 253 characters"
	}
	if strings.Contains(host, "*") {
		return "wildcard_unsupported", "wildcard domains are not supported"
	}
	if strings.Contains(host, "/") || strings.Contains(host, "@") || strings.Contains(host, " ") {
		return "invalid_domain", "domain must be a bare hostname, without scheme, path, or credentials"
	}
	if ip := net.ParseIP(host); ip != nil {
		return "invalid_domain", "domain must be a hostname, not an IP address"
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "invalid_domain", "domain must contain at least one dot (e.g. example.com)"
	}
	for _, label := range labels {
		if label == "" {
			return "invalid_domain", "domain contains an empty label"
		}
		if len(label) > 63 {
			return "invalid_domain", "domain label is longer than 63 characters"
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "invalid_domain", "domain label must not start or end with a hyphen"
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "invalid_domain", "domain may only contain letters, digits, hyphens, and dots"
			}
		}
	}

	for _, zone := range reservedZones {
		suffix := Normalize(zone)
		if suffix == "" {
			continue
		}
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return "reserved_domain", "hosts under " + suffix + " belong to the platform and cannot be attached"
		}
	}
	return "", ""
}
