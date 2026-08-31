package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"snaphost/internal/control/db"
)

// ErrBusy prevents deletion while an in-process build or provisioning step can
// still create Docker resources after the cleanup snapshot was taken.
var ErrBusy = errors.New("project has an in-flight deploy")

// Summary is one project as the operator's project screen shows it: enough to
// decide whether deleting it is safe without opening it first.
//
// RunningCount is the one that matters. Deleting a project stops live sites,
// and a count of zero is the difference between reclaiming disk and taking
// something down.
type Summary struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	Slug         string     `json:"slug"`
	SourceKey    string     `json:"source_key"`
	DeploysCount int64      `json:"deploys_count"`
	RunningCount int64      `json:"running_count"`
	StoppedCount int64      `json:"stopped_count"`
	DomainsCount int64      `json:"domains_count"`
	LastDeployAt *time.Time `json:"last_deploy_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// DeployResource is the external state that has to be gone before a project
// and its history can be deleted.
type DeployResource struct {
	DeployID     uuid.UUID
	Status       string
	ContainerID  string
	ImageRef     string
	ImageDeleted bool
	SagaStep     string
}

// Deletion is the cleanup plan loaded before an audited project deletion.
type Deletion struct {
	Project      Summary
	Deploys      []DeployResource
	DomainsCount int64
}

const summaryColumns = `
    p.id, p.user_id, p.slug, p.source_key,
    (SELECT count(*) FROM deploys d WHERE d.project_id = p.id AND d.status <> 'deleted'),
    (SELECT count(*) FROM deploys d WHERE d.project_id = p.id AND d.status = 'running'),
    (SELECT count(*) FROM deploys d WHERE d.project_id = p.id AND d.status = 'stopped'),
    (SELECT count(*) FROM custom_domains cd WHERE cd.project_id = p.id AND cd.status = 'verified'),
    (SELECT max(d.created_at) FROM deploys d WHERE d.project_id = p.id),
    p.created_at`

func scanSummary(row interface{ Scan(...any) error }) (Summary, error) {
	var s Summary
	err := row.Scan(&s.ID, &s.UserID, &s.Slug, &s.SourceKey,
		&s.DeploysCount, &s.RunningCount, &s.StoppedCount, &s.DomainsCount,
		db.IntoNull(&s.LastDeployAt), db.Into(&s.CreatedAt))
	return s, err
}

// ListSummaries returns an account's projects with the counters the screen
// decides on, newest first.
//
// The counters are correlated subqueries over whole tables, which is the same
// bargain the rest of the control plane makes: at one operator's scale the
// tables are small, and a query someone can read beats one that is fast on
// data this installation does not have.
func (r *Repository) ListSummaries(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT`+summaryColumns+`
		 FROM projects p
		 WHERE p.user_id = ?
		 ORDER BY p.created_at DESC`, userID.String())
	if err != nil {
		return nil, fmt.Errorf("list project summaries: %w", err)
	}
	defer rows.Close()

	out := []Summary{}
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project summary: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project summaries: %w", err)
	}
	return out, nil
}

// PrepareDeletion loads the complete external cleanup plan.
//
// It refuses a project while any deploy or saga is still building or
// provisioning: that work runs in-process and could create a container after
// this snapshot, which the caller would then never see and never clean up.
func (r *Repository) PrepareDeletion(ctx context.Context, projectID uuid.UUID) (*Deletion, error) {
	var plan Deletion
	err := scanSummaryInto(r.db.QueryRowContext(ctx,
		`SELECT`+summaryColumns+` FROM projects p WHERE p.id = ?`, projectID.String()), &plan.Project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("prepare project deletion: %w", err)
	}
	plan.DomainsCount = plan.Project.DomainsCount

	rows, err := r.db.QueryContext(ctx, `
		SELECT d.id, d.status, coalesce(d.container_id, ''), coalesce(d.image_ref, ''),
		       d.image_deleted_at IS NOT NULL, coalesce(s.current_step, '')
		FROM deploys d
		LEFT JOIN deploy_sagas s ON s.deploy_id = d.id
		WHERE d.project_id = ?
		ORDER BY d.created_at`, projectID.String())
	if err != nil {
		return nil, fmt.Errorf("list project cleanup resources: %w", err)
	}
	defer rows.Close()

	plan.Deploys = []DeployResource{}
	for rows.Next() {
		var resource DeployResource
		if err := rows.Scan(&resource.DeployID, &resource.Status, &resource.ContainerID,
			&resource.ImageRef, &resource.ImageDeleted, &resource.SagaStep); err != nil {
			return nil, fmt.Errorf("scan project cleanup resource: %w", err)
		}
		if resourceInFlight(resource) {
			return nil, ErrBusy
		}
		plan.Deploys = append(plan.Deploys, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project cleanup resources: %w", err)
	}
	return &plan, nil
}

func scanSummaryInto(row interface{ Scan(...any) error }, out *Summary) error {
	s, err := scanSummary(row)
	if err != nil {
		return err
	}
	*out = s
	return nil
}

func resourceInFlight(resource DeployResource) bool {
	switch resource.Status {
	case "pending", "building", "provisioning":
		return true
	}
	switch resource.SagaStep {
	case "pending", "building", "built", "provisioning", "compensating":
		return true
	}
	return false
}

// placeholders renders n positional markers. SQLite has no array binding, and
// an IN list with the wrong count is an execution-time error rather than a
// compile-time one.
// It is never called with zero: an empty IN () is a syntax error and an
// IN (NULL) is a silent always-false, so the caller drops the clause instead.
func placeholders(n int) string {
	out := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',', ' ')
		}
		out = append(out, '?')
	}
	return string(out)
}

func idArgs(ids []uuid.UUID) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// Delete removes a fully cleaned project and its history, recording the
// operator action in the same transaction as the removal.
//
// plannedDeploys is the set the caller's cleanup plan enumerated and acted on.
// It is required rather than re-derived: the point of the check below is to
// notice that the world moved between the plan and this commit, which a fresh
// read of the same table cannot do.
//
// deploys.project_id is ON DELETE SET NULL, so dropping the project alone
// would orphan its builds rather than remove them — each table is deleted
// explicitly, in an order that leaves no saga row pointing at a deploy that is
// gone.
func (r *Repository) Delete(ctx context.Context, projectID, actorUserID uuid.UUID, plannedDeploys []uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete project: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		ownerID     string
		slug        string
		deployCount int64
		domainCount int64
	)
	err = tx.QueryRowContext(ctx, `
		SELECT p.user_id, p.slug,
		       (SELECT count(*) FROM deploys d WHERE d.project_id = p.id),
		       (SELECT count(*) FROM custom_domains cd WHERE cd.project_id = p.id)
		FROM projects p WHERE p.id = ?`, projectID.String(),
	).Scan(&ownerID, &slug, &deployCount, &domainCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read project before delete: %w", err)
	}

	// The re-check is a whole-project assertion, not a status filter, and that
	// difference is the race.
	//
	// Filtering on pending/building/provisioning missed the deploy that
	// finished during the cleanup window: a build that reaches 'running'
	// between the plan snapshot and this commit has a deploy status and a saga
	// step that are both terminal, so neither list matched it — and its rows
	// were deleted while its container kept running, with nothing left naming
	// the container.
	//
	// That window is not small. Cleanup stops containers and removes images
	// and is allowed five minutes.
	//
	// So the condition is: nothing about this project may have moved. Every
	// deploy must still be one the plan enumerated, and none of them may be
	// live — the caller has just stopped every container the plan listed, so a
	// running deploy here is by definition one the plan never saw.
	// The empty plan is its own branch, and it has to be. `id NOT IN (NULL)`
	// evaluates to NULL in SQLite, not TRUE — NULL is not a value anything
	// compares unequal to — so the row is filtered out and the count comes
	// back zero. A project that had no deploys when the plan was taken would
	// therefore accept any deploy created during the window. The in-flight
	// check catches one that is still live, but a build that raced through to
	// 'failed' is terminal on both columns and would have slipped past.
	unplannedQuery := `SELECT count(*) FROM deploys WHERE project_id = ?`
	args := []any{projectID.String()}
	if len(plannedDeploys) > 0 {
		unplannedQuery += ` AND id NOT IN (` + placeholders(len(plannedDeploys)) + `)`
		args = append(args, idArgs(plannedDeploys)...)
	}

	var unplanned int
	if err := tx.QueryRowContext(ctx, unplannedQuery, args...).Scan(&unplanned); err != nil {
		return fmt.Errorf("check for deploys created during cleanup: %w", err)
	}
	if unplanned > 0 {
		return ErrBusy
	}

	var inFlight int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*)
		FROM deploys d
		LEFT JOIN deploy_sagas s ON s.deploy_id = d.id
		WHERE d.project_id = ?
		  AND (d.status IN ('pending', 'building', 'provisioning', 'running')
		       OR s.current_step IN ('pending', 'building', 'built', 'provisioning', 'compensating'))`,
		projectID.String(),
	).Scan(&inFlight); err != nil {
		return fmt.Errorf("check project deletion state: %w", err)
	}
	if inFlight > 0 {
		return ErrBusy
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM deploy_sagas WHERE deploy_id IN (SELECT id FROM deploys WHERE project_id = ?)`,
		projectID.String()); err != nil {
		return fmt.Errorf("delete project sagas: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_domains WHERE project_id = ?`, projectID.String()); err != nil {
		return fmt.Errorf("delete project domains: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deploys WHERE project_id = ?`, projectID.String()); err != nil {
		return fmt.Errorf("delete project deploys: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID.String())
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	// The counts are the point of the details column: after this commit there
	// is nothing left to count, so what is not captured here is lost.
	details, err := json.Marshal(map[string]any{
		"owner_user_id": ownerID,
		"slug":          slug,
		"deploys":       deployCount,
		"domains":       domainCount,
	})
	if err != nil {
		return fmt.Errorf("encode project deletion audit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_audit_log (id, actor_user_id, action, target_type, target_id, details)
		VALUES (?, ?, 'project.delete', 'project', ?, ?)`,
		uuid.NewString(), actorUserID.String(), projectID.String(), string(details)); err != nil {
		return fmt.Errorf("audit project deletion: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project deletion: %w", err)
	}
	return nil
}

// AuditEntry is one recorded operator action. There is no foreign key to
// users: the point of an audit row is to outlive whatever it describes.
type AuditEntry struct {
	ID          uuid.UUID       `json:"id"`
	ActorUserID uuid.UUID       `json:"actor_user_id"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Details     json.RawMessage `json:"details"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ListAudit returns the most recent operator actions, newest first.
func (r *Repository) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, actor_user_id, action, target_type, target_id, details, created_at
		 FROM admin_audit_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	defer rows.Close()

	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var details string
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.Action, &e.TargetType, &e.TargetID,
			&details, db.Into(&e.CreatedAt)); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		e.Details = json.RawMessage(details)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit rows: %w", err)
	}
	return out, nil
}
