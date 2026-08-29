package domain

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		" Example.COM ":   "example.com",
		"example.com.":    "example.com",
		"example.com:80":  "example.com",
		"WWW.Example.Com": "www.example.com",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// platformZones mirrors production: user deploys answer on one registrable
// domain, the dashboard and API on another. Neither is attachable.
var platformZones = []string{"snaphost.pw", "snaphost.ru"}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		wantCode string
	}{
		{"platform subdomain", "abc123.snaphost.pw", "reserved_domain"},
		{"platform apex", "snaphost.pw", "reserved_domain"},
		{"control plane apex", "snaphost.ru", "reserved_domain"},
		{"control plane api", "api.snaphost.ru", "reserved_domain"},
		{"wildcard", "*.example.com", "wildcard_unsupported"},
		{"single label", "localhost", "invalid_domain"},
		{"ip address", "203.0.113.10", "invalid_domain"},
		{"url not hostname", "https://example.com/path", "invalid_domain"},
		{"empty label", "example..com", "invalid_domain"},
		{"leading hyphen", "-bad.example.com", "invalid_domain"},
		{"underscore", "bad_label.example.com", "invalid_domain"},
		{"empty", "", "invalid_domain"},
		{"too long", strings.Repeat("a.", 130) + "com", "invalid_domain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, message := Validate(Normalize(tc.host), platformZones)
			if code != tc.wantCode {
				t.Fatalf("Validate(%q) code = %q (%q), want %q", tc.host, code, message, tc.wantCode)
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	hosts := []string{
		"example.com",
		"www.example.com",
		"my-site.co.uk",
		"a1.b2.example.com",
		// Similar to ours but not ours: suffix matching must be on labels, not
		// on string prefixes.
		"snaphost.pw.example.com",
		"notsnaphost.ru",
	}
	for _, host := range hosts {
		if code, message := Validate(Normalize(host), platformZones); code != "" {
			t.Fatalf("Validate(%q) rejected with %s: %s", host, code, message)
		}
	}
}

// A deployment with no reserved zones configured must still validate hostnames;
// it just has nothing of its own to protect.
func TestValidateWithoutReservedZones(t *testing.T) {
	if code, _ := Validate("example.com", nil); code != "" {
		t.Fatalf("valid host rejected with %s", code)
	}
	if code, _ := Validate("localhost", nil); code != "invalid_domain" {
		t.Fatalf("single-label host must still be rejected, got %q", code)
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	b, _ := NewToken()
	if a == b {
		t.Fatal("verification tokens must not repeat")
	}
	if !strings.HasPrefix(a, "snaphost-verify=") {
		t.Fatalf("token %q should be self-describing in a DNS record", a)
	}
}

// --- verifier -------------------------------------------------------------

type fakeStore struct {
	verified []uuid.UUID
	failed   map[uuid.UUID]string
	touched  map[uuid.UUID]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{failed: map[uuid.UUID]string{}, touched: map[uuid.UUID]string{}}
}

func (f *fakeStore) DueForCheck(context.Context, time.Duration, int) ([]Domain, error) {
	return nil, nil
}
func (f *fakeStore) MarkVerified(_ context.Context, id uuid.UUID) error {
	f.verified = append(f.verified, id)
	return nil
}
func (f *fakeStore) MarkFailed(_ context.Context, id uuid.UUID, reason string) error {
	f.failed[id] = reason
	return nil
}
func (f *fakeStore) TouchChecked(_ context.Context, id uuid.UUID, reason string) error {
	f.touched[id] = reason
	return nil
}

type fakeResolver struct {
	records []string
	err     error
}

func (f fakeResolver) LookupTXT(context.Context, string) ([]string, error) {
	return f.records, f.err
}

func newTestDomain(status string) Domain {
	return Domain{
		ID:                uuid.New(),
		Domain:            "example.com",
		VerificationToken: "snaphost-verify=token",
		Status:            status,
	}
}

func verifierWith(store verifierStore, r checker) *Verifier {
	return &Verifier{store: store, resolver: r, log: zap.NewNop(), BatchSize: 10}
}

func TestVerifierMarksVerifiedOnMatchingRecord(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	v := verifierWith(store, fakeResolver{records: []string{"unrelated", " snaphost-verify=token "}})

	v.check(context.Background(), d)

	if len(store.verified) != 1 || store.verified[0] != d.ID {
		t.Fatalf("expected domain to be verified, got %v (failed: %v)", store.verified, store.failed)
	}
}

func TestVerifierFailsOnWrongValue(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	v := verifierWith(store, fakeResolver{records: []string{"snaphost-verify=someone-elses-token"}})

	v.check(context.Background(), d)

	if _, ok := store.failed[d.ID]; !ok {
		t.Fatal("a non-matching TXT value must not verify the domain")
	}
}

func TestVerifierFailsVerifiedDomainWhenRecordDisappears(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusVerified)
	v := verifierWith(store, fakeResolver{err: &net.DNSError{Err: "no such host", IsNotFound: true}})

	v.check(context.Background(), d)

	if _, ok := store.failed[d.ID]; !ok {
		t.Fatal("a verified domain whose record is gone must lose verification (dangling-record takeover)")
	}
}

func TestVerifierKeepsStatusOnTransientResolverError(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusVerified)
	v := verifierWith(store, fakeResolver{err: errors.New("i/o timeout")})

	v.check(context.Background(), d)

	if len(store.failed) != 0 {
		t.Fatal("a transient resolver failure must not take a live site down")
	}
	if _, ok := store.touched[d.ID]; !ok {
		t.Fatal("transient failure should still record that a check happened")
	}
}

// --- propagation grace ----------------------------------------------------
// A domain attached a minute ago whose DNS has not caught up is not a failure;
// it is the normal state of a fresh attach. Calling it "Проверка не прошла"
// tells the user something is broken when nothing is, which is exactly the
// report that prompted this behaviour.

func notFoundVerifier(store verifierStore, grace time.Duration) *Verifier {
	v := verifierWith(store, fakeResolver{err: &net.DNSError{Err: "no such host", IsNotFound: true}})
	v.PropagationGrace = grace
	return v
}

func TestVerifierKeepsFreshDomainPendingWhileDNSPropagates(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	d.CreatedAt = time.Now().Add(-2 * time.Minute)

	notFoundVerifier(store, time.Hour).check(context.Background(), d)

	if _, failed := store.failed[d.ID]; failed {
		t.Fatal("a two-minute-old domain must not be marked failed for a record DNS has not published yet")
	}
	if reason := store.touched[d.ID]; reason != ReasonTXTNotFound {
		t.Fatalf("expected the reason to be recorded as %q so the dashboard can explain the wait, got %q",
			ReasonTXTNotFound, reason)
	}
}

func TestVerifierFailsOnceTheGraceWindowHasPassed(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	d.CreatedAt = time.Now().Add(-25 * time.Hour)

	notFoundVerifier(store, 24*time.Hour).check(context.Background(), d)

	if store.failed[d.ID] != ReasonTXTNotFound {
		t.Fatal("past the window a missing record is a real failure, and saying so is what makes it actionable")
	}
}

// The takeover case must not be softened: a domain that once verified is
// serving traffic, so losing its record has to stop it at once regardless of
// how recently it was attached.
func TestVerifierGraceNeverAppliesToAVerifiedDomain(t *testing.T) {
	for _, d := range []Domain{
		func() Domain { x := newTestDomain(StatusVerified); x.CreatedAt = time.Now(); return x }(),
		func() Domain {
			x := newTestDomain(StatusPending)
			x.CreatedAt = time.Now()
			now := time.Now()
			x.VerifiedAt = &now
			return x
		}(),
	} {
		store := newFakeStore()
		notFoundVerifier(store, 24*time.Hour).check(context.Background(), d)
		if _, ok := store.failed[d.ID]; !ok {
			t.Fatalf("a domain that has verified before must fail immediately (status=%s, verified_at=%v)",
				d.Status, d.VerifiedAt)
		}
	}
}

// A record that exists with the wrong value is not a propagation delay: the
// user can fix it now, so it stays a failure however fresh the domain is.
func TestVerifierGraceDoesNotCoverAMismatchedValue(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	d.CreatedAt = time.Now()
	v := verifierWith(store, fakeResolver{records: []string{"snaphost-verify=someone-elses-token"}})
	v.PropagationGrace = 24 * time.Hour

	v.check(context.Background(), d)

	if store.failed[d.ID] != ReasonTXTMismatch {
		t.Fatal("a wrong TXT value is actionable immediately and must not be hidden behind the grace window")
	}
}

// Grace of zero keeps the old behaviour, so the window can be turned off.
func TestVerifierZeroGraceFailsImmediately(t *testing.T) {
	store := newFakeStore()
	d := newTestDomain(StatusPending)
	d.CreatedAt = time.Now()

	notFoundVerifier(store, 0).check(context.Background(), d)

	if _, ok := store.failed[d.ID]; !ok {
		t.Fatal("a zero grace window must fail on the first miss")
	}
}
