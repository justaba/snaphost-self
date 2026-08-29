package admin

import (
	"context"

	"testing"

	"database/sql"
	"path/filepath"

	"github.com/google/uuid"

	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// These tests run the real queries against a real database, because that is
// the only thing that can catch a wrong column name, a scan order that drifted
// from its SELECT list, or a join that silently drops rows — none of which the
// handler tests can see.
//
// They used to need a PostgreSQL container and an ADMIN_TEST_DATABASE_URL, so
// in practice they ran when someone remembered and never in CI. On SQLite the
// database is a file in a temp directory, so they run like any other test —
// which is the point of the engine change as much as the memory is.
func testPool(t *testing.T) *sql.DB {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })

	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return handle
}

// seed inserts one account with a wallet, a project, two deploys, a saga, the
// three ledger entries a deploy produces, a verified domain, and an API key.
// Everything is namespaced by fresh UUIDs, so parallel runs do not collide and
// nothing has to be cleaned up between tests.
type seeded struct {
	userID   uuid.UUID
	email    string
	project  uuid.UUID
	deployOK uuid.UUID
	deployKO uuid.UUID
	domain   uuid.UUID
}

func seed(t *testing.T, pool *sql.DB) seeded {
	t.Helper()
	ctx := context.Background()

	s := seeded{
		userID:   uuid.New(),
		project:  uuid.New(),
		deployOK: uuid.New(),
		deployKO: uuid.New(),
		domain:   uuid.New(),
	}
	s.email = "op-" + s.userID.String()[:8] + "@example.test"

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, sql, args...); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}

	exec(`INSERT INTO users (id, email) VALUES (?, ?)`, s.userID.String(), s.email)
	exec(`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, ?, ?)`,
		s.project.String(), s.userID.String(), "demo-app", "git:github.com/acme/demo#main")

	exec(`INSERT INTO deploys (id, user_id, project_id, source_type, repo_url, branch, status,
	          subdomain, endpoint_url, container_id)
	      VALUES ($1, $2, $3, 'git_public', 'https://github.com/acme/demo', 'main', 'running',
	              $4, 'https://proj.example.test', 'container-1')`,
		s.deployOK.String(), s.userID.String(), s.project.String(), "proj-"+s.deployOK.String()[:12])

	exec(`INSERT INTO deploys (id, user_id, project_id, source_type, status,
	          failure_reason, upload_id)
	      VALUES ($1, $2, $3, 'archive', 'failed',
	              'the container started but nothing answered on PORT', 'upload-1')`,
		s.deployKO.String(), s.userID.String(), s.project.String())

	exec(`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, image_built,
	          container_running, retry_count, failure_reason)
	      VALUES ($1, $2, 'failed', true, false, 2, 'probe_failed')`,
		s.deployKO.String(), s.userID.String())

	exec(`INSERT INTO custom_domains (id, user_id, project_id, target_deploy_id, domain,
	          verification_token, status, verified_at)
	      VALUES (?, ?, ?, ?, ?, 'token', 'verified', ?)`,
		s.domain.String(), s.userID.String(), s.project.String(), s.deployOK.String(),
		"app-"+s.userID.String()[:8]+".example.test", controldb.Now())

	exec(`INSERT INTO api_keys (user_id, key_hash, key_prefix, name)
	      VALUES ($1, $2, 'sk_live_abcd', 'mcp')`, s.userID, "hash-"+s.userID.String())

	return s
}

func TestIntegration_Overview(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	seed(t, pool)

	o, err := repo.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	// Absolute values depend on what else is in the database, so assert the
	// invariants the screen relies on rather than exact counts.
	if o.Users == 0 || o.Deploys == 0 || o.Projects == 0 {
		t.Fatalf("overview came back empty: %+v", o)
	}
	if o.DeploysRunning == 0 || o.DeploysFailed == 0 {
		t.Errorf("status counters did not pick up the seeded deploys: %+v", o)
	}
	if o.DomainsActive == 0 {
		t.Errorf("verified domain not counted: %+v", o)
	}
	if o.ActiveAPIKeys == 0 {
		t.Errorf("api key not counted: %+v", o)
	}
}

func TestIntegration_ListAndGetUser(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()

	page, err := repo.ListUsers(ctx, Filter{Query: s.email, Limit: 10})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("search by email returned %d rows (total %d)", len(page.Items), page.Total)
	}

	got := page.Items[0]
	if got.ID != s.userID {
		t.Fatalf("wrong user: %s", got.ID)
	}
	if got.Email == nil || *got.Email != s.email {
		t.Errorf("email not joined: %v", got.Email)
	}
	if got.DeploysTotal != 2 || got.DeploysRunning != 1 || got.DeploysFailed != 1 {
		t.Errorf("deploy rollup wrong: total=%d running=%d failed=%d",
			got.DeploysTotal, got.DeploysRunning, got.DeploysFailed)
	}
	if got.DomainsCount != 1 {
		t.Errorf("domain rollup wrong: %d", got.DomainsCount)
	}
	if got.LastDeployAt == nil {
		t.Error("last_deploy_at not populated")
	}

	// Searching by the raw UUID has to work too — that is what an operator
	// arriving from a log line has in hand.
	byID, err := repo.ListUsers(ctx, Filter{Query: s.userID.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list users by id: %v", err)
	}
	if byID.Total != 1 {
		t.Fatalf("UUID search returned %d rows", byID.Total)
	}

	detail, err := repo.GetUser(ctx, s.userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if len(detail.Projects) != 1 || detail.Projects[0].DeploysCount != 2 {
		t.Errorf("projects wrong: %+v", detail.Projects)
	}
	if len(detail.Domains) != 1 || len(detail.APIKeys) != 1 {
		t.Errorf("attached lists wrong: domains=%d keys=%d", len(detail.Domains), len(detail.APIKeys))
	}
	if len(detail.APIKeys) == 1 && detail.APIKeys[0].Prefix != "sk_live_abcd" {
		t.Errorf("key prefix wrong: %q", detail.APIKeys[0].Prefix)
	}

	if _, err := repo.GetUser(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("missing user: got %v want ErrNotFound", err)
	}
}

func TestIntegration_DeploysAndDetail(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()

	page, err := repo.ListDeploys(ctx, Filter{UserID: &s.userID, Limit: 10})
	if err != nil {
		t.Fatalf("list deploys: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("expected 2 deploys, got %d", page.Total)
	}
	for _, d := range page.Items {
		if d.UserEmail == nil || *d.UserEmail != s.email {
			t.Errorf("deploy %s missing joined email", d.ID)
		}
		if d.ProjectSlug == nil || *d.ProjectSlug != "demo-app" {
			t.Errorf("deploy %s missing joined project slug", d.ID)
		}
	}

	// The status filter and the free-text filter have to compose.
	running, err := repo.ListDeploys(ctx, Filter{UserID: &s.userID, Status: "running", Limit: 10})
	if err != nil {
		t.Fatalf("filter by status: %v", err)
	}
	if running.Total != 1 || running.Items[0].ID != s.deployOK {
		t.Fatalf("status filter wrong: total=%d", running.Total)
	}

	byText, err := repo.ListDeploys(ctx, Filter{Query: s.deployKO.String(), Limit: 10})
	if err != nil {
		t.Fatalf("filter by text: %v", err)
	}
	if byText.Total != 1 || byText.Items[0].ID != s.deployKO {
		t.Fatalf("text filter wrong: total=%d", byText.Total)
	}

	detail, err := repo.GetDeploy(ctx, s.deployKO)
	if err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if detail.Saga == nil {
		t.Fatal("saga not loaded")
	}
	if detail.Saga.CurrentStep != "failed" || detail.Saga.RetryCount != 2 || !detail.Saga.ImageBuilt {
		t.Errorf("saga fields wrong: %+v", detail.Saga)
	}
	if detail.Saga.ContainerRunning {
		t.Errorf("saga flags should be false for a failed deploy: %+v", detail.Saga)
	}
	if len(detail.Domains) != 0 {
		t.Errorf("failed deploy should carry no alias: %+v", detail.Domains)
	}

	live, err := repo.GetDeploy(ctx, s.deployOK)
	if err != nil {
		t.Fatalf("get running deploy: %v", err)
	}
	if len(live.Domains) != 1 || live.Domains[0].ID != s.domain {
		t.Errorf("alias not resolved onto its deploy: %+v", live.Domains)
	}
	// A deploy with no saga row is a real state, and must not be an error.
	if live.Saga != nil {
		t.Errorf("unexpected saga on the seeded running deploy: %+v", live.Saga)
	}

	if _, err := repo.GetDeploy(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("missing deploy: got %v want ErrNotFound", err)
	}
}

func TestIntegration_Domains(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()

	domains, err := repo.ListDomains(ctx, Filter{UserID: &s.userID, Status: "verified", Limit: 10})
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	if domains.Total != 1 {
		t.Fatalf("expected 1 domain, got %d", domains.Total)
	}
	got := domains.Items[0]
	if got.TargetDeployID == nil || *got.TargetDeployID != s.deployOK {
		t.Errorf("alias target wrong: %v", got.TargetDeployID)
	}
	if got.UserEmail == nil {
		t.Error("domain row missing joined email")
	}
	if got.VerifiedAt == nil {
		t.Error("verified_at not populated")
	}
}

// Paging has to be stable and the total independent of the window, or the
// pager renders "51–100 из 50".
func TestIntegration_PagingIsConsistent(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()

	first, err := repo.ListDeploys(ctx, Filter{UserID: &s.userID, Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	second, err := repo.ListDeploys(ctx, Filter{UserID: &s.userID, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if first.Total != second.Total {
		t.Fatalf("total changed between pages: %d vs %d", first.Total, second.Total)
	}
	if len(first.Items) != 1 || len(second.Items) != 1 {
		t.Fatalf("page sizes wrong: %d and %d", len(first.Items), len(second.Items))
	}
	if first.Items[0].ID == second.Items[0].ID {
		t.Fatal("the same deploy appeared on both pages")
	}
}

// An account that has never deployed anything must still appear in the list
// with zeroed rollups. Every aggregate is a LEFT JOIN for exactly this reason,
// and one of them turned into an inner join is invisible until the operator
// goes looking for an account that has done nothing yet — which is precisely
// when they are trying to work out whether signup worked at all.
func TestIntegration_AccountWithNoActivityIsListed(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	orphan := uuid.New()
	email := "orphan-" + orphan.String()[:8] + "@example.test"
	if _, err := pool.ExecContext(ctx, `INSERT INTO users (id, email) VALUES (?, ?)`, orphan.String(), email); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	page, err := repo.ListUsers(ctx, Filter{Query: email, Limit: 10})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("account with no activity not listed: total=%d", page.Total)
	}
	got := page.Items[0]
	if got.DeploysTotal != 0 || got.DomainsCount != 0 {
		t.Errorf("rollups should be zero, got deploys=%d domains=%d", got.DeploysTotal, got.DomainsCount)
	}
	if got.LastDeployAt != nil {
		t.Errorf("last_deploy_at should be null, got %v", *got.LastDeployAt)
	}
}
