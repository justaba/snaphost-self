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

// This platform has no billing. The upstream project carried a `user` policy
// line for a topup endpoint that minted credit from a request body, and it
// stayed that way for months — so the policy file is asserted to admit nothing
// billing-shaped at all, rather than to authenticate one particular path.
func TestNoBillingPathIsReachableByAnyRole(t *testing.T) {
	e := newEnforcer(t)

	paths := []string{
		"/api/v1/billing",
		"/api/v1/billing/topup",
		"/api/v1/billing/reserve",
	}
	for _, role := range []string{"guest", "user", "admin"} {
		for _, path := range paths {
			for _, method := range []string{"GET", "POST"} {
				if mustEnforce(t, e, role, path, method) {
					t.Errorf("role %q may %s %s", role, method, path)
				}
			}
		}
	}
}

// There is no /api/v1/admin surface any more. It existed so one role could
// read across every account, which is a multi-tenant SaaS's problem; here the
// operator's own projects are all the projects.
//
// This is asserted as an absence rather than deleted along with the routes,
// because a policy line outliving its handler is exactly how the panel ended
// up with a Транзакции tab pointing at a page that no longer existed.
func TestNoAdminSurfaceRemains(t *testing.T) {
	e := newEnforcer(t)

	paths := []string{
		"/api/v1/admin",
		"/api/v1/admin/overview",
		"/api/v1/admin/users",
		"/api/v1/admin/users/11111111-2222-3333-4444-555555555555",
		"/api/v1/admin/deploys",
		"/api/v1/admin/deploys/11111111-2222-3333-4444-555555555555",
		"/api/v1/admin/domains",
		"/api/v1/admin/projects",
		"/api/v1/admin/projects/11111111-2222-3333-4444-555555555555",
	}
	for _, role := range []string{"guest", "user", "admin"} {
		for _, path := range paths {
			for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
				if mustEnforce(t, e, role, path, method) {
					t.Errorf("role %q may %s %s — the admin surface was removed", role, method, path)
				}
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

// Project management moved onto the ordinary dashboard, so these are `user`
// lines now. Nothing is reachable without a session at all — `guest` is the
// role an unauthenticated request gets.
func TestProjectManagementIsReachableBySignedInRoles(t *testing.T) {
	e := newEnforcer(t)

	const list = "/api/v1/projects"
	const item = "/api/v1/projects/11111111-2222-3333-4444-555555555555"
	const start = "/api/v1/deploys/11111111-2222-3333-4444-555555555555/start"

	for _, role := range []string{"user", "admin"} {
		if !mustEnforce(t, e, role, list, "GET") {
			t.Errorf("role %q cannot list projects; the screen would 403", role)
		}
		if !mustEnforce(t, e, role, item, "DELETE") {
			t.Errorf("role %q cannot delete a project", role)
		}
		if !mustEnforce(t, e, role, start, "POST") {
			t.Errorf("role %q cannot start a stopped deploy", role)
		}
	}
	for _, path := range []string{list, item, start} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			if mustEnforce(t, e, "guest", path, method) {
				t.Errorf("guest may %s %s", method, path)
			}
		}
	}
}

// keyMatch2's :id must not let the collection path borrow the item path's
// DELETE. Addressing the list must never become a way to delete.
func TestProjectCollectionRejectsMutations(t *testing.T) {
	e := newEnforcer(t)

	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		if mustEnforce(t, e, "admin", "/api/v1/projects", method) {
			t.Errorf("admin may %s the project collection", method)
		}
	}
}

// The audit log records destructive actions, so reading it stays behind the
// admin role even though everything it records is now on the user surface.
func TestAuditLogIsAdminOnly(t *testing.T) {
	e := newEnforcer(t)
	const path = "/api/v1/audit"

	if !mustEnforce(t, e, "admin", path, "GET") {
		t.Fatal("admin cannot read the audit log")
	}
	for _, role := range []string{"guest", "user"} {
		if mustEnforce(t, e, role, path, "GET") {
			t.Errorf("role %q may read the audit log", role)
		}
	}
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		if mustEnforce(t, e, "admin", path, method) {
			t.Errorf("admin may %s the audit log; it is append-only from the code that writes it", method)
		}
	}
}
