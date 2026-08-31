package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// Project deletion is the one destructive action the panel has, so the two
// properties that make it admissible are tested against a real database: it is
// refused while work is in flight that could create a container afterwards,
// and the audit row is committed by the same transaction as the removal, so
// there is no window where the rows are gone and the record of who removed
// them is not.

func testPool(t *testing.T) *sql.DB {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return handle
}

type seeded struct {
	userID    uuid.UUID
	project   uuid.UUID
	deployRun uuid.UUID
	deployKO  uuid.UUID
	domain    uuid.UUID
}

// seed inserts one account with a project, a running deploy, a failed deploy
// with a saga, and a verified domain. Everything is namespaced by fresh UUIDs
// so two seeds in one test do not collide.
func seed(t *testing.T, pool *sql.DB) seeded {
	t.Helper()
	ctx := context.Background()

	s := seeded{
		userID:    uuid.New(),
		project:   uuid.New(),
		deployRun: uuid.New(),
		deployKO:  uuid.New(),
		domain:    uuid.New(),
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}

	exec(`INSERT INTO users (id, email) VALUES (?, ?)`,
		s.userID.String(), "op-"+s.userID.String()[:8]+"@example.test")
	exec(`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, ?, ?)`,
		s.project.String(), s.userID.String(), "demo-app", "git:github.com/acme/demo#main")
	exec(`INSERT INTO deploys (id, user_id, project_id, source_type, repo_url, status, subdomain, container_id, image_ref)
	      VALUES (?, ?, ?, 'git_public', 'https://github.com/acme/demo', 'running', ?, 'container-1', 'snaphost/proj-abc:ok')`,
		s.deployRun.String(), s.userID.String(), s.project.String(), "proj-"+s.deployRun.String()[:12])
	exec(`INSERT INTO deploys (id, user_id, project_id, source_type, status, image_ref)
	      VALUES (?, ?, ?, 'archive', 'failed', 'snaphost/proj-abc:ko')`,
		s.deployKO.String(), s.userID.String(), s.project.String())
	exec(`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'failed')`,
		s.deployKO.String(), s.userID.String())
	exec(`INSERT INTO custom_domains (id, user_id, project_id, target_deploy_id, domain, verification_token, status, verified_at)
	      VALUES (?, ?, ?, ?, ?, 'token', 'verified', ?)`,
		s.domain.String(), s.userID.String(), s.project.String(), s.deployRun.String(),
		"app-"+s.userID.String()[:8]+".example.test", controldb.Now())

	return s
}

// planned is the deploy set a cleanup plan over this seed would have
// enumerated. Delete takes it because the commit has to notice deploys that
// appeared after the plan, which re-reading the table cannot do.
func planned(s seeded) []uuid.UUID {
	return []uuid.UUID{s.deployRun, s.deployKO}
}

// afterCleanup puts the seed into the state the handler leaves it in before it
// commits: every container in the plan has been stopped. Delete refuses a
// project with a running deploy, and that refusal is the point — a deploy that
// is still live at commit time is one the cleanup never touched.
func afterCleanup(t *testing.T, pool *sql.DB, s seeded) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(),
		`UPDATE deploys SET status = 'stopped' WHERE id = ?`, s.deployRun.String()); err != nil {
		t.Fatalf("simulate cleanup: %v", err)
	}
}

// RunningCount is what the screen decides on: deleting a project with a
// running deploy takes a live site down, without one it only reclaims disk.
// StoppedCount is what says the project can be brought back cheaply.
func TestListSummariesCountsWhatTheScreenDecidesOn(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)

	items, err := repo.ListSummaries(context.Background(), s.userID)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d projects, want the one seeded", len(items))
	}
	got := items[0]
	if got.ID != s.project {
		t.Errorf("project id = %v, want %v", got.ID, s.project)
	}
	if got.DeploysCount != 2 {
		t.Errorf("DeploysCount = %d, want 2", got.DeploysCount)
	}
	if got.RunningCount != 1 {
		t.Errorf("RunningCount = %d, want 1 — this is the count that says a live site goes with it", got.RunningCount)
	}
	if got.StoppedCount != 0 {
		t.Errorf("StoppedCount = %d, want 0", got.StoppedCount)
	}
	if got.DomainsCount != 1 {
		t.Errorf("DomainsCount = %d, want the verified domain", got.DomainsCount)
	}
	if got.LastDeployAt == nil {
		t.Error("LastDeployAt is null for a project with two deploys")
	}
}

// The counters are correlated subqueries. If one loses its WHERE, every
// project reports the whole table's totals and they all look identical.
func TestListSummariesCountsPerProject(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	a := seed(t, pool)
	b := seed(t, pool)

	for _, owner := range []seeded{a, b} {
		items, err := repo.ListSummaries(context.Background(), owner.userID)
		if err != nil {
			t.Fatalf("ListSummaries: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("account %v sees %d projects, want only its own", owner.userID, len(items))
		}
		if items[0].DeploysCount != 2 || items[0].RunningCount != 1 || items[0].DomainsCount != 1 {
			t.Errorf("project %v counted %d/%d/%d, want 2/1/1 — a subquery is counting the whole table",
				items[0].ID, items[0].DeploysCount, items[0].RunningCount, items[0].DomainsCount)
		}
	}
}

// A deleted deploy is gone as far as the operator is concerned, so counting it
// would show a project as busier than it is and make "0 builds" unreachable.
func TestListSummariesIgnoresDeletedDeploys(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)

	if _, err := pool.ExecContext(context.Background(),
		`UPDATE deploys SET status = 'deleted' WHERE id = ?`, s.deployKO.String()); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	items, err := repo.ListSummaries(context.Background(), s.userID)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if items[0].DeploysCount != 1 {
		t.Errorf("DeploysCount = %d, want deleted builds excluded", items[0].DeploysCount)
	}
}

// The refusal exists because the build queue is in-process: a saga at 'built'
// is about to create a container, and a plan taken now would not mention it.
func TestPrepareDeletionRefusesInFlightWork(t *testing.T) {
	for _, tc := range []struct{ name, status, saga string }{
		{"deploy is building", "building", "failed"},
		{"deploy is pending", "pending", "failed"},
		{"deploy is provisioning", "provisioning", "failed"},
		{"saga is mid-build", "failed", "building"},
		{"saga has an image and no container yet", "failed", "built"},
		{"saga is compensating", "failed", "compensating"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPool(t)
			repo := NewRepository(pool)
			s := seed(t, pool)
			ctx := context.Background()

			if _, err := pool.ExecContext(ctx,
				`UPDATE deploys SET status = ? WHERE id = ?`, tc.status, s.deployKO.String()); err != nil {
				t.Fatalf("set status: %v", err)
			}
			if _, err := pool.ExecContext(ctx,
				`UPDATE deploy_sagas SET current_step = ? WHERE deploy_id = ?`, tc.saga, s.deployKO.String()); err != nil {
				t.Fatalf("set saga step: %v", err)
			}

			if _, err := repo.PrepareDeletion(ctx, s.project); err != ErrBusy {
				t.Fatalf("PrepareDeletion = %v, want ErrBusy", err)
			}
			// The same refusal must hold at commit time, or the check is only
			// advisory and the window between plan and delete is unguarded.
			if err := repo.Delete(ctx, s.project, uuid.New(), planned(s)); err != ErrBusy {
				t.Fatalf("Delete = %v, want ErrBusy", err)
			}
		})
	}
}

func TestPrepareDeletionListsExternalState(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)

	plan, err := repo.PrepareDeletion(context.Background(), s.project)
	if err != nil {
		t.Fatalf("PrepareDeletion: %v", err)
	}
	if plan.Project.UserID != s.userID {
		t.Errorf("plan owner = %v, want %v; the handler checks this before deleting", plan.Project.UserID, s.userID)
	}
	if len(plan.Deploys) != 2 {
		t.Fatalf("plan covers %d deploys, want both", len(plan.Deploys))
	}
	byID := map[uuid.UUID]DeployResource{}
	for _, d := range plan.Deploys {
		byID[d.DeployID] = d
	}
	if got := byID[s.deployRun].ContainerID; got != "container-1" {
		t.Errorf("running deploy came back with container_id %q; losing it strands a container", got)
	}
	if got := byID[s.deployRun].ImageRef; got != "snaphost/proj-abc:ok" {
		t.Errorf("running deploy came back with image_ref %q", got)
	}
	if byID[s.deployKO].ContainerID != "" {
		t.Error("failed deploy reported a container it never had")
	}
	if got := byID[s.deployKO].SagaStep; got != "failed" {
		t.Errorf("saga step = %q, want failed from the join", got)
	}
}

func TestDeleteRemovesEverythingAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()
	actor := uuid.New()

	afterCleanup(t, pool, s)
	if err := repo.Delete(ctx, s.project, actor, planned(s)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// deploys.project_id is ON DELETE SET NULL, so dropping the project alone
	// would orphan its builds rather than remove them.
	for _, check := range []struct{ what, query string }{
		{"project", `SELECT count(*) FROM projects WHERE id = ?`},
		{"deploys", `SELECT count(*) FROM deploys WHERE project_id = ?`},
		{"domains", `SELECT count(*) FROM custom_domains WHERE project_id = ?`},
	} {
		var n int
		if err := pool.QueryRowContext(ctx, check.query, s.project.String()).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", check.what, err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows survived", check.what, n)
		}
	}
	var orphanSagas int
	if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM deploy_sagas WHERE deploy_id IN (?, ?)`,
		s.deployRun.String(), s.deployKO.String()).Scan(&orphanSagas); err != nil {
		t.Fatalf("count sagas: %v", err)
	}
	if orphanSagas != 0 {
		t.Errorf("%d saga rows outlived their deploys", orphanSagas)
	}

	entries, err := repo.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d audit rows, want exactly one", len(entries))
	}
	e := entries[0]
	if e.ActorUserID != actor {
		t.Errorf("audit actor = %v, want %v", e.ActorUserID, actor)
	}
	if e.Action != "project.delete" || e.TargetID != s.project.String() {
		t.Errorf("audit row = %s on %s", e.Action, e.TargetID)
	}

	// The counts are the point of the details column: after the commit there
	// is nothing left to count, so uncaptured is lost.
	var details struct {
		OwnerUserID string  `json:"owner_user_id"`
		Slug        string  `json:"slug"`
		Deploys     float64 `json:"deploys"`
		Domains     float64 `json:"domains"`
	}
	if err := json.Unmarshal(e.Details, &details); err != nil {
		t.Fatalf("audit details are not JSON: %v (%s)", err, e.Details)
	}
	if details.OwnerUserID != s.userID.String() || details.Slug != "demo-app" {
		t.Errorf("audit details = %+v", details)
	}
	if details.Deploys != 2 || details.Domains != 1 {
		t.Errorf("audit counts = %v deploys / %v domains, want 2 and 1", details.Deploys, details.Domains)
	}
}

// The delete statements are keyed on project_id. A missing predicate on any
// one of them empties the table for everybody.
func TestDeleteLeavesOtherProjectsAlone(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	victim := seed(t, pool)
	bystander := seed(t, pool)
	ctx := context.Background()

	afterCleanup(t, pool, victim)
	if err := repo.Delete(ctx, victim.project, uuid.New(), planned(victim)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, check := range []struct {
		what, query string
		want        int
	}{
		{"projects", `SELECT count(*) FROM projects WHERE id = ?`, 1},
		{"deploys", `SELECT count(*) FROM deploys WHERE project_id = ?`, 2},
		{"domains", `SELECT count(*) FROM custom_domains WHERE project_id = ?`, 1},
	} {
		var n int
		if err := pool.QueryRowContext(ctx, check.query, bystander.project.String()).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", check.what, err)
		}
		if n != check.want {
			t.Errorf("bystander %s: %d rows, want %d", check.what, n, check.want)
		}
	}
}

func TestDeleteRejectsUnknownProjectAndWritesNoAudit(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	if _, err := repo.PrepareDeletion(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("PrepareDeletion = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, uuid.New(), uuid.New(), nil); err != ErrNotFound {
		t.Errorf("Delete = %v, want ErrNotFound", err)
	}
	entries, err := repo.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d audit rows written for a deletion that was refused", len(entries))
	}
}

// The audit screen reads newest first; an operator looking for what just
// happened should not have to page to the end.
func TestListAuditIsNewestFirst(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	first := seed(t, pool)
	second := seed(t, pool)
	afterCleanup(t, pool, first)
	afterCleanup(t, pool, second)
	if err := repo.Delete(ctx, first.project, uuid.New(), planned(first)); err != nil {
		t.Fatalf("Delete first: %v", err)
	}
	if err := repo.Delete(ctx, second.project, uuid.New(), planned(second)); err != nil {
		t.Fatalf("Delete second: %v", err)
	}

	entries, err := repo.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d audit rows, want 2", len(entries))
	}
	if entries[0].CreatedAt.Before(entries[1].CreatedAt) {
		t.Errorf("audit rows came back oldest first: %v then %v", entries[0].CreatedAt, entries[1].CreatedAt)
	}
}

// The race this guards is the reason Delete takes the planned set at all.
//
// A build that completes during the cleanup window ends at deploy status
// 'running' and saga step 'running'. Both are terminal, so a status-filter
// re-check saw nothing wrong and deleted the rows while the container ran —
// leaving a container with nothing in the database naming it, which no sweep
// can find because every sweep works from those rows.
//
// The window is not small: cleanup stops containers and removes images under a
// five-minute budget.
func TestDeleteRefusesADeployThatAppearedDuringCleanup(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)
	ctx := context.Background()

	plan, err := repo.PrepareDeletion(ctx, s.project)
	if err != nil {
		t.Fatalf("PrepareDeletion: %v", err)
	}
	if len(plan.Deploys) != 2 {
		t.Fatalf("plan covers %d deploys", len(plan.Deploys))
	}
	afterCleanup(t, pool, s)

	// A whole build lands between the plan and the commit and reaches running.
	latecomer := uuid.New()
	if _, err := pool.ExecContext(ctx, `
		INSERT INTO deploys (id, user_id, project_id, source_type, status, container_id, image_ref)
		VALUES (?, ?, ?, 'git_public', 'running', 'container-late', 'snaphost/proj-abc:late')`,
		latecomer.String(), s.userID.String(), s.project.String()); err != nil {
		t.Fatalf("seed the latecomer: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'running')`,
		latecomer.String(), s.userID.String()); err != nil {
		t.Fatalf("seed the latecomer saga: %v", err)
	}

	if err := repo.Delete(ctx, s.project, uuid.New(), planned(s)); err != ErrBusy {
		t.Fatalf("Delete = %v, want ErrBusy — the container nothing stopped would have been orphaned", err)
	}

	// And nothing may have been removed by the refused attempt.
	var deploys int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM deploys WHERE project_id = ?`, s.project.String()).Scan(&deploys); err != nil {
		t.Fatalf("count deploys: %v", err)
	}
	if deploys != 3 {
		t.Errorf("%d deploys survived a refused deletion, want all 3", deploys)
	}
}

// A deploy still running at commit time is one the cleanup never stopped, even
// when it was in the plan — the caller stops every planned container before
// committing, so this state means something went wrong upstream.
func TestDeleteRefusesWhileAnyDeployIsStillRunning(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	s := seed(t, pool)

	// No afterCleanup: deployRun is left running.
	if err := repo.Delete(context.Background(), s.project, uuid.New(), planned(s)); err != ErrBusy {
		t.Fatalf("Delete = %v, want ErrBusy", err)
	}
}

// The empty plan is its own SQL branch, and getting it wrong is silent.
//
// `id NOT IN (NULL)` evaluates to NULL in SQLite rather than TRUE — NULL is
// not a value anything compares unequal to — so the row is filtered out and
// the count comes back zero. A project with no deploys at plan time would
// therefore have accepted anything created during the cleanup window. The
// running/in-flight check covers a deploy that is still live; a build that
// raced through to 'failed' is terminal on both columns and slipped past.
func TestDeleteWithAnEmptyPlanStillNoticesALatecomer(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	owner := uuid.New()
	projectID := uuid.New()
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO users (id, email) VALUES (?, ?)`, owner.String(), "op@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, 'empty', 'git:x#main')`,
		projectID.String(), owner.String()); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	plan, err := repo.PrepareDeletion(ctx, projectID)
	if err != nil {
		t.Fatalf("PrepareDeletion: %v", err)
	}
	if len(plan.Deploys) != 0 {
		t.Fatalf("plan covers %d deploys, want none", len(plan.Deploys))
	}

	// A build lands during the window and fails fast. Terminal on both the
	// deploy status and the saga step, and holding an image.
	late := uuid.New()
	if _, err := pool.ExecContext(ctx, `
		INSERT INTO deploys (id, user_id, project_id, source_type, status, image_ref)
		VALUES (?, ?, ?, 'git_public', 'failed', 'snaphost/proj-abc:late')`,
		late.String(), owner.String(), projectID.String()); err != nil {
		t.Fatalf("seed the latecomer: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'failed')`,
		late.String(), owner.String()); err != nil {
		t.Fatalf("seed the latecomer saga: %v", err)
	}

	if err := repo.Delete(ctx, projectID, uuid.New(), nil); err != ErrBusy {
		t.Fatalf("Delete = %v, want ErrBusy — the latecomer's image would have been orphaned", err)
	}

	var deploys int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM deploys WHERE project_id = ?`, projectID.String()).Scan(&deploys); err != nil {
		t.Fatalf("count deploys: %v", err)
	}
	if deploys != 1 {
		t.Errorf("%d deploys after a refused deletion, want the latecomer kept", deploys)
	}
}

// A genuinely empty project still deletes. The check has to refuse latecomers
// without refusing the ordinary case.
func TestDeleteAnEmptyProjectSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	owner := uuid.New()
	projectID := uuid.New()
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO users (id, email) VALUES (?, ?)`, owner.String(), "op@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, 'empty', 'git:x#main')`,
		projectID.String(), owner.String()); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	if err := repo.Delete(ctx, projectID, uuid.New(), nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM projects WHERE id = ?`, projectID.String()).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("an empty project was not deleted")
	}
}
