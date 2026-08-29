package routes

import (
	"testing"

	"github.com/casbin/casbin/v2"
)

// The policy file is data, and the middleware enforces it against the real URL
// path. These tests read the shipped file so a wrong or missing line fails here
// rather than in production.
func newEnforcer(t *testing.T) *casbin.Enforcer {
	t.Helper()

	e, err := casbin.NewEnforcer("../rbac_model.conf", "../rbac_policy.csv")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return e
}

func mustEnforce(t *testing.T, e *casbin.Enforcer, role, path, method string) bool {
	t.Helper()

	allowed, err := e.Enforce(role, path, method)
	if err != nil {
		t.Fatalf("enforce %s %s %s: %v", role, method, path, err)
	}
	return allowed
}

// Crediting a wallet was a `user` policy line until 2026-08-07, which let any
// authenticated account mint itself coins. The route is internal now; the
// policy must never admit it again.
func TestTopupIsNotReachableByAnyRole(t *testing.T) {
	e := newEnforcer(t)

	for _, role := range []string{"guest", "user", "admin"} {
		if mustEnforce(t, e, role, "/api/v1/billing/topup", "POST") {
			t.Errorf("role %q may POST /api/v1/billing/topup", role)
		}
	}
}

func TestBillingReadStaysAllowed(t *testing.T) {
	e := newEnforcer(t)

	if !mustEnforce(t, e, "user", "/api/v1/billing", "GET") {
		t.Error("a user may no longer read their own wallet")
	}
}

// The operator console is admin-only, and `admin` inherits `user` rather than
// the other way round.
func TestAdminRoutesAreAdminOnly(t *testing.T) {
	adminPaths := []string{
		"/api/v1/admin/overview",
		"/api/v1/admin/users",
		"/api/v1/admin/users/11111111-2222-3333-4444-555555555555",
		"/api/v1/admin/users/11111111-2222-3333-4444-555555555555/transactions",
		"/api/v1/admin/deploys",
		"/api/v1/admin/deploys/11111111-2222-3333-4444-555555555555",
		"/api/v1/admin/transactions",
		"/api/v1/admin/domains",
	}

	e := newEnforcer(t)

	for _, path := range adminPaths {
		if !mustEnforce(t, e, "admin", path, "GET") {
			t.Errorf("admin cannot GET %s — missing policy line", path)
		}
		for _, role := range []string{"guest", "user"} {
			if mustEnforce(t, e, role, path, "GET") {
				t.Errorf("role %q may GET %s", role, path)
			}
		}
	}
}

// An admin is also an ordinary user; the inheritance line is what makes the
// dashboard work for them at all.
func TestAdminInheritsUser(t *testing.T) {
	e := newEnforcer(t)

	if !mustEnforce(t, e, "admin", "/api/v1/deploys", "GET") {
		t.Error("admin lost the inherited user permissions")
	}
}

// Admin access is read-only in this pass: no policy line may admit a method
// that changes state, or the console can mutate without an audit trail.
func TestAdminConsoleIsReadOnly(t *testing.T) {
	e := newEnforcer(t)

	paths := []string{
		"/api/v1/admin/users",
		"/api/v1/admin/users/11111111-2222-3333-4444-555555555555",
		"/api/v1/admin/deploys/11111111-2222-3333-4444-555555555555",
	}

	for _, path := range paths {
		for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
			if mustEnforce(t, e, "admin", path, method) {
				t.Errorf("admin may %s %s — the console is meant to be read-only", method, path)
			}
		}
	}
}
