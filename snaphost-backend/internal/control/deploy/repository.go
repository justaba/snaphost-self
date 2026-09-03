// Package deploy provides data access for the deploys table.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"database/sql"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

// Deploy source types. The source abstraction (Task 14b) decides how the
// builder obtains the project: cloning a public repo, cloning a private
// repo with a short-lived credential (14b-3), or unpacking an uploaded
// archive blob (14b-2).
const (
	SourceGitPublic  = "git_public"
	SourceGitPrivate = "git_private"
	SourceArchive    = "archive"
)

// ValidSourceType reports whether s is a known deploy source type.
func ValidSourceType(s string) bool {
	switch s {
	case SourceGitPublic, SourceGitPrivate, SourceArchive:
		return true
	}
	return false
}

// Deploy represents a user deployment record. A deploy is immutable once
// built: it is one build result, permanently addressable at its own
// subdomain. The mutable publish target above it is the project (Task 16a),
// and the pointer between them is an alias in custom_domains.
type Deploy struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	ProjectID      *uuid.UUID `json:"project_id,omitempty"`
	SourceType     string     `json:"source_type"`
	RepoURL        string     `json:"repo_url"`
	Branch         string     `json:"branch"`
	UploadID       *string    `json:"upload_id,omitempty"`
	CommitSHA      *string    `json:"commit_sha,omitempty"`
	Status         string     `json:"status"`
	ImageRef       *string    `json:"image_ref,omitempty"`
	EndpointURL    *string    `json:"endpoint_url,omitempty"`
	Subdomain      *string    `json:"subdomain,omitempty"`
	ContainerID    *string    `json:"container_id,omitempty"`
	TTLExpiresAt   *time.Time `json:"ttl_expires_at,omitempty"`
	ImageDeletedAt *time.Time `json:"image_deleted_at,omitempty"`
	FailureReason  *string    `json:"failure_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

// GCPolicy configures what the reclaim sweep is allowed to take away from a
// project beyond plain TTL expiry (Task 16a items 7 and 8). Both limits are
// disabled when zero.
type GCPolicy struct {
	// KeepPerProject is how many superseded deploys of a project stay
	// running as rollback targets. Older ones are reclaimed — registry
	// storage, not idle runtime, is the accumulating cost.
	KeepPerProject int
	// StoppedImageGraceHours is how long a stopped deploy keeps the image it
	// can be restarted from before that image becomes disk to reclaim.
	//
	// It exists because stopping deliberately does not release the image —
	// that is what makes Start a container run instead of a rebuild — and
	// without an expiry on the favour, every TTL expiry would leak a build's
	// worth of disk permanently. Nothing else moves a deploy out of 'stopped':
	// the only other path to 'deleted' is an operator pressing the button.
	StoppedImageGraceHours int
}

// Repository provides CRUD data access for the deploys table.
type Repository struct {
	db *sql.DB
	gc GCPolicy
	// logArchiver, when set, is asked for a deploy's log tail as it fails.
	logArchiver LogArchiver
}

// Option configures a Repository at construction.
type Option func(*Repository)

// WithGCPolicy sets the reclaim limits applied on top of TTL expiry.
func WithGCPolicy(p GCPolicy) Option {
	return func(r *Repository) { r.gc = p }
}

// StatusFailed is the terminal status whose log output is worth keeping. It is
// spelled as a literal in a dozen other places; this constant exists where the
// value decides behaviour rather than merely describing it.
const StatusFailed = "failed"

// LogArchiver supplies the log output still held in memory for a deploy that
// has just failed. It returns an opaque blob — this package stores it and does
// not read it, so the encoding is settled between the adapter that produces it
// and the reader that serves it back.
type LogArchiver interface {
	Archive(deployID string) []byte
}

// WithLogArchiver makes UpdateStatus capture the log tail on the transition to
// 'failed'.
//
// It hangs off the repository rather than off the three callers because this is
// where they converge: the saga marks a deploy failed, so does the build
// pipeline through its status reporter, and so does the runtime when a
// container will not come up. Three call sites would be three chances to add a
// fourth and forget.
func WithLogArchiver(a LogArchiver) Option {
	return func(r *Repository) { r.logArchiver = a }
}

// NewRepository creates a new deploy repository.
func NewRepository(handle *sql.DB, opts ...Option) *Repository {
	r := &Repository{db: handle}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Create inserts a new deploy record. An empty RepoURL is stored as NULL
// (archive deploys have no repository).
func (r *Repository) Create(ctx context.Context, d Deploy) error {
	if d.SourceType == "" {
		d.SourceType = SourceGitPublic
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deploys (id, user_id, project_id, source_type, repo_url, branch, upload_id, commit_sha, status, metadata)
		 VALUES (?, ?, ?, ?, nullif(?, ''), ?, ?, ?, ?, '{}')`,
		d.ID.String(), d.UserID.String(), uuidPtr(d.ProjectID), d.SourceType, d.RepoURL, d.Branch, d.UploadID, d.CommitSHA, d.Status,
	)
	if err != nil {
		return fmt.Errorf("create deploy: %w", err)
	}
	return nil
}

// deployColumns is the shared SELECT list for full Deploy rows; scanDeploy
// consumes it in the same order.
const deployColumns = `id, user_id, project_id, source_type, coalesce(repo_url, ''), branch, upload_id, commit_sha, status,
	        image_ref, endpoint_url, subdomain, container_id, ttl_expires_at,
	        image_deleted_at, failure_reason, created_at, updated_at, stopped_at`

// rowScanner is satisfied by both sql.Row and sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDeploy(row rowScanner) (Deploy, error) {
	var d Deploy
	err := row.Scan(&d.ID, &d.UserID, &d.ProjectID, &d.SourceType, &d.RepoURL, &d.Branch, &d.UploadID, &d.CommitSHA, &d.Status,
		&d.ImageRef, &d.EndpointURL, &d.Subdomain, &d.ContainerID,
		controldb.IntoNull(&d.TTLExpiresAt), controldb.IntoNull(&d.ImageDeletedAt), &d.FailureReason,
		controldb.Into(&d.CreatedAt), controldb.Into(&d.UpdatedAt), controldb.IntoNull(&d.StoppedAt))
	return d, err
}

// Get retrieves a single deploy by ID.
func (r *Repository) Get(ctx context.Context, deployID uuid.UUID) (*Deploy, error) {
	d, err := scanDeploy(r.db.QueryRowContext(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE id = ?`,
		deployID.String(),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("deploy not found: %w", err)
		}
		return nil, fmt.Errorf("get deploy: %w", err)
	}
	return &d, nil
}

// ListByUser returns paginated deploys for a user, ordered by creation time descending.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Deploy, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE user_id = ?
		 ORDER BY created_at DESC
		 LIMIT ? OFFSET ?`,
		userID.String(), limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys by user: %w", err)
	}
	return collectDeploys(rows)
}

// ListByProject returns a project's deploys newest first — the rollback
// candidates for an alias, each still addressable at its own subdomain.
func (r *Repository) ListByProject(ctx context.Context, projectID uuid.UUID, limit int) ([]Deploy, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE project_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys by project: %w", err)
	}
	return collectDeploys(rows)
}

func collectDeploys(rows *sql.Rows) ([]Deploy, error) {
	defer rows.Close()

	var deploys []Deploy
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy row: %w", err)
		}
		deploys = append(deploys, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deploy rows: %w", err)
	}

	return deploys, nil
}

// UpdateStatus transitions a deploy to a new status with an optional failure reason.
func (r *Repository) UpdateStatus(ctx context.Context, deployID uuid.UUID, status string, failureReason *string) error {
	now := controldb.Now()
	result, err := r.db.ExecContext(ctx,
		updateStatusSQL,
		status, failureReason, status, now, now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("update deploy status: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	r.archiveLogs(ctx, deployID, status)
	return nil
}

// archiveLogs stores the log tail of a deploy that has just failed.
//
// Best-effort on purpose, and separate from the status write rather than part
// of it: the status is what the platform acts on and the logs are what a person
// reads afterwards, so a failure to save the second must not undo the first. A
// deploy stuck in its old status because its logs could not be written would be
// a worse outcome than a failed deploy with no logs.
//
// It is also idempotent by construction: the same status may be written twice
// — the runtime marks a deploy failed and the saga does too — and the second
// pass simply overwrites the column with the same tail.
func (r *Repository) archiveLogs(ctx context.Context, deployID uuid.UUID, status string) {
	if r.logArchiver == nil || status != StatusFailed {
		return
	}
	tail := r.logArchiver.Archive(deployID.String())
	if len(tail) == 0 {
		return
	}
	_, _ = r.db.ExecContext(ctx,
		`UPDATE deploys SET log_tail = ? WHERE id = ?`, tail, deployID.String())
}

// LogTail returns the archived log output of a failed deploy, or nil when
// there is none — the deploy succeeded, is still running, or predates the
// archive. A missing deploy is nil rather than an error: the caller is serving
// log history and has already established the deploy exists.
func (r *Repository) LogTail(ctx context.Context, deployID uuid.UUID) ([]byte, error) {
	var tail []byte
	err := r.db.QueryRowContext(ctx,
		`SELECT log_tail FROM deploys WHERE id = ?`, deployID.String(),
	).Scan(&tail)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read log tail: %w", err)
	}
	return tail, nil
}

// The status is bound twice and the timestamp twice: SQLite's placeholders are
// positional, so each use needs its own.
const updateStatusSQL = `UPDATE deploys
 SET status = ?,
     failure_reason = ?,
     stopped_at = CASE
         WHEN ? = 'stopped' THEN COALESCE(stopped_at, ?)
         ELSE stopped_at
     END,
     updated_at = ?
 WHERE id = ?`

// SetRunning atomically publishes the runtime details to both durable views of
// a deployment. The deploy row is what the API and router read; the saga row is
// what resume and compensation read. Committing only one of them can either
// expose an untracked container or make a saga terminal while the deploy still
// appears to be provisioning.
func (r *Repository) SetRunning(ctx context.Context, deployID uuid.UUID, imageRef, endpointURL, subdomain, containerID string, ttlExpiresAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set deploy running: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := controldb.Now()
	result, err := tx.ExecContext(ctx,
		// stopped_at is cleared, and that matters more than it looks. It is
		// written with a COALESCE so it records the *first* stop and never
		// moves, which was right when nothing could restart a deploy. Now that
		// something can, a stale stopped_at is the clock the image-reclaim
		// grace runs on: a deploy stopped in January, restarted, and stopped
		// again in June would be measured from January and have its image
		// taken immediately. A running deploy has no stop time.
		`UPDATE deploys
		 SET status = 'running', image_ref = ?, endpoint_url = ?, subdomain = ?,
		     container_id = ?, ttl_expires_at = ?, image_deleted_at = NULL,
		     stopped_at = NULL, updated_at = ?
		 WHERE id = ? AND status IN ('building', 'provisioning', 'running')`,
		imageRef, endpointURL, subdomain, containerID,
		controldb.NullTime(ttlExpiresAt), now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("set deploy running: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("deploy not found or not runnable: %s", deployID)
	}

	// Keep an already-finalized saga terminal on an idempotent call. For the
	// normal built -> provisioning transition, persist the runtime handle before
	// returning success so a crash can resume at stepFinalize without starting a
	// second container.
	result, err = tx.ExecContext(ctx,
		`UPDATE deploy_sagas
		 SET current_step = CASE WHEN current_step = 'running' THEN 'running' ELSE 'provisioning' END,
		     container_running = 1,
		     container_id = ?,
		     endpoint_url = ?,
		     started_at = COALESCE(started_at, ?)
		 WHERE deploy_id = ? AND current_step IN ('built', 'provisioning', 'running')`,
		containerID, endpointURL, now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("record running container in saga: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("runnable saga not found: %s", deployID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set deploy running: %w", err)
	}
	return nil
}

// SagaView is the orchestration state behind a deploy: what actually happened,
// as against deploys.status, which is what the operator is shown.
//
// It used to be visible only on an admin screen, and it is the only place in
// the UI that answers "why is this stuck" — a saga at 'compensating' with a
// retry count is a different problem from one that never left 'pending'.
type SagaView struct {
	CurrentStep      string  `json:"current_step"`
	RetryCount       int     `json:"retry_count"`
	ImageBuilt       bool    `json:"image_built"`
	ContainerRunning bool    `json:"container_running"`
	AppPort          *int    `json:"app_port,omitempty"`
	FailureReason    *string `json:"failure_reason,omitempty"`
}

// GetSaga returns the saga behind a deploy, or nil when there is none. A
// missing saga is not an error: rows predating the orchestrator have none, and
// the screen simply omits the section.
func (r *Repository) GetSaga(ctx context.Context, deployID uuid.UUID) (*SagaView, error) {
	var v SagaView
	var appPort *int64
	err := r.db.QueryRowContext(ctx,
		`SELECT current_step, retry_count, image_built, container_running, app_port, failure_reason
		 FROM deploy_sagas WHERE deploy_id = ?`, deployID.String(),
	).Scan(&v.CurrentStep, &v.RetryCount, &v.ImageBuilt, &v.ContainerRunning, &appPort, &v.FailureReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get deploy saga: %w", err)
	}
	if appPort != nil {
		p := int(*appPort)
		v.AppPort = &p
	}
	return &v, nil
}

// RestartTarget is everything Start needs to put a stopped deploy back on its
// feet without rebuilding: the artifact and the port the build detected.
type RestartTarget struct {
	UserID   uuid.UUID
	ImageRef string
	Port     int
}

// ErrDeployNotFound is a restart aimed at a deploy that is not there.
var ErrDeployNotFound = errors.New("deploy not found")

// ErrNotRestartable is a deploy whose image is gone. It is a refusal rather
// than a rebuild, because rebuilding is a different operation with a different
// cost and the operator should be the one choosing it.
var ErrNotRestartable = errors.New("deploy image has been reclaimed")

// ErrNotStopped is a deploy that is not in the one state Start applies to.
var ErrNotStopped = errors.New("deploy is not stopped")

// BeginRestart claims a stopped deploy for a restart and returns what the
// runtime needs to run it.
//
// The status move to 'provisioning' is the claim, and it is a guarded UPDATE
// rather than a read followed by a write: two clicks on the button race
// otherwise, and the loser would start a second container for a deploy whose
// row only records one. Exactly one caller sees a row affected.
//
// The port comes from the saga, which is where the build recorded what EXPOSE
// said. Losing it would mean probing a port nothing listens on and tearing the
// container straight back down.
func (r *Repository) BeginRestart(ctx context.Context, deployID uuid.UUID, defaultPort int) (*RestartTarget, error) {
	var (
		target   RestartTarget
		imageRef *string
		deleted  *string
		status   string
		appPort  *int64
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT d.user_id, d.status, d.image_ref, d.image_deleted_at, s.app_port
		FROM deploys d
		LEFT JOIN deploy_sagas s ON s.deploy_id = d.id
		WHERE d.id = ?`, deployID.String(),
	).Scan(&target.UserID, &status, &imageRef, &deleted, &appPort)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeployNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read deploy for restart: %w", err)
	}

	if status != "stopped" {
		return nil, ErrNotStopped
	}
	if imageRef == nil || strings.TrimSpace(*imageRef) == "" || deleted != nil {
		return nil, ErrNotRestartable
	}
	target.ImageRef = *imageRef

	target.Port = defaultPort
	if appPort != nil && *appPort > 0 {
		target.Port = int(*appPort)
	}

	now := controldb.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploys SET status = 'provisioning', updated_at = ?
		 WHERE id = ? AND status = 'stopped'`, now, deployID.String())
	if err != nil {
		return nil, fmt.Errorf("claim deploy for restart: %w", err)
	}
	if rowsAffected(result) == 0 {
		// Someone else claimed it between the read and this update.
		return nil, ErrNotStopped
	}
	return &target, nil
}

// AbandonRestart puts a claimed deploy back to stopped after the runtime
// refused it. Without this a failed start leaves the row at 'provisioning'
// forever: nothing sweeps that status, the button disappears, and the deploy
// looks like it is starting for the rest of the installation's life.
func (r *Repository) AbandonRestart(ctx context.Context, deployID uuid.UUID, reason string) error {
	now := controldb.Now()
	var failure *string
	if strings.TrimSpace(reason) != "" {
		failure = &reason
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE deploys SET status = 'stopped', failure_reason = ?, updated_at = ?
		 WHERE id = ? AND status = 'provisioning'`,
		failure, now, deployID.String())
	if err != nil {
		return fmt.Errorf("abandon restart: %w", err)
	}
	return nil
}

// MarkDeleted transitions a deploy to deleted and records when it stopped.
func (r *Repository) MarkDeleted(ctx context.Context, deployID uuid.UUID) error {
	now := controldb.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploys
		 SET status = 'deleted', stopped_at = ?, updated_at = ?
		 WHERE id = ?`,
		now, now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("mark deploy deleted: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

// MarkImageDeleted records that the deploy's local Docker artifact no longer
// occupies disk. image_ref remains intact for diagnostics and audit; the
// timestamp is the durable idempotency marker used by the cleanup sweep.
func (r *Repository) MarkImageDeleted(ctx context.Context, deployID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploys SET image_deleted_at = COALESCE(image_deleted_at, ?), updated_at = ? WHERE id = ?`,
		controldb.Now(), controldb.Now(), deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("mark deploy image deleted: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

// SetImageRef records the artifact a build just loaded into the daemon.
//
// It is called immediately after the load and before anything else can fail,
// because the image sweep works entirely from this column: an image the
// database cannot name is one nothing will ever collect.
//
// image_deleted_at is cleared for the same reason SetRunning clears it: a
// stale marker on a freshly built artifact would hide it from the sweep.
func (r *Repository) SetImageRef(ctx context.Context, deployID uuid.UUID, imageRef string) error {
	if strings.TrimSpace(imageRef) == "" {
		return nil
	}
	now := controldb.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploys SET image_ref = ?, image_deleted_at = NULL, updated_at = ? WHERE id = ?`,
		imageRef, now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("record built image: %w", err)
	}
	if rowsAffected(result) == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

// ReclaimStoppedDeploys moves stopped deploys past their grace period to
// 'deleted', which is what puts their images into the cleanup queue.
//
// This closes the only automatic route out of 'stopped'. The watchdog's TTL
// sweep stops an expired deploy and the retention sweep stops a superseded
// one; both leave the row at 'stopped', and the image sweep deliberately
// skips that status so the deploy stays startable. Without this the two facts
// combine into a leak with no upper bound: every deploy that ever expired
// keeps a container image on the host's disk forever, which is precisely the
// disk-growth problem image GC was added to solve.
//
// A deploy an alias publishes is never touched, on the same principle as the
// TTL sweep: someone's hostname points at it.
func (r *Repository) ReclaimStoppedDeploys(ctx context.Context, limit int) (int, error) {
	if r.gc.StoppedImageGraceHours <= 0 {
		return 0, nil
	}
	now := controldb.Now()
	cutoff := controldb.FormatTime(
		time.Now().UTC().Add(-time.Duration(r.gc.StoppedImageGraceHours) * time.Hour))

	// stopped_at, and deliberately not a COALESCE onto updated_at. There is a
	// trigger that rewrites updated_at on every UPDATE, so it tracks the last
	// write rather than the stop — a row touched for any reason would have its
	// grace clock reset and might never age out. Every path that reaches
	// 'stopped' sets stopped_at (UpdateStatus does it in the same statement),
	// so requiring it costs nothing and means the comparison is against the
	// time the sweep actually cares about.
	result, err := r.db.ExecContext(ctx, `
		UPDATE deploys
		SET status = 'deleted', updated_at = ?
		WHERE id IN (
		    SELECT d.id FROM deploys d
		    WHERE d.status = 'stopped'
		      AND d.stopped_at IS NOT NULL
		      AND d.stopped_at < ?
		      AND NOT EXISTS (
		          SELECT 1 FROM custom_domains cd
		          WHERE cd.target_deploy_id = d.id AND cd.status = 'verified')
		    LIMIT ?
		)`, now, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("reclaim stopped deploys: %w", err)
	}
	return int(rowsAffected(result)), nil
}

// ImageCleanup identifies one terminal deploy whose local image still needs
// removal. The watchdog retries these rows until MarkImageDeleted succeeds.
type ImageCleanup struct {
	ID       uuid.UUID
	ImageRef string
}

// FindImagesPendingCleanup returns dead deploy images not yet confirmed absent
// from Docker. It also picks up rows created before image GC existed.
//
// 'stopped' is deliberately not in this list, and that is the whole reason
// starting a stopped deploy is cheap. A stopped deploy is one an operator
// turned off or whose TTL ran out; its image is what lets it come back in
// seconds instead of a rebuild, so the disk it holds is the price of that
// button. 'failed' and 'deleted' are the ones nothing can ever start again —
// a failed build has no working container to recover and a deleted deploy is
// not coming back — so those are the images with nothing to lose.
//
// Stopped images are still reclaimed, just later: the retention and idle-alias
// sweeps move an old deploy to 'deleted', and this query gets it then.
func (r *Repository) FindImagesPendingCleanup(ctx context.Context, limit int) ([]ImageCleanup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, image_ref
		 FROM deploys
		 WHERE status IN ('failed', 'deleted')
		   AND image_ref IS NOT NULL AND image_ref <> ''
		   AND image_deleted_at IS NULL
		 ORDER BY updated_at
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("find deploy images pending cleanup: %w", err)
	}
	defer rows.Close()

	var images []ImageCleanup
	for rows.Next() {
		var image ImageCleanup
		if err := rows.Scan(&image.ID, &image.ImageRef); err != nil {
			return nil, fmt.Errorf("scan deploy image cleanup: %w", err)
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deploy image cleanup: %w", err)
	}
	return images, nil
}

// aliasPinned is true for a deploy an alias currently publishes. Such a
// deploy is the live site on someone's own hostname, so plain TTL expiry must
// never take it away — only the idle sweep below can, and only after it has
// been unpinned first.
const aliasPinned = `EXISTS (
	    SELECT 1 FROM custom_domains cd
	    WHERE cd.target_deploy_id = deploys.id AND cd.status = 'verified')`

// FindExpired returns IDs of running, un-aliased deploys whose TTL has expired.
func (r *Repository) FindExpired(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM deploys
		 WHERE status = 'running' AND ttl_expires_at < ? AND NOT `+aliasPinned+`
		 LIMIT ?`,
		controldb.Now(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("find expired deploys: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired deploy id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired deploy ids: %w", err)
	}

	return ids, nil
}

// ExpiredDeploy holds the minimal fields needed to stop an expired container.
type ExpiredDeploy struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	ContainerID *string   `json:"container_id"`
}

// reclaimableSQL selects everything the watchdog may stop. Three sources
// converge on one list, so the watchdog itself needs no new endpoint:
//
//  1. TTL expiry of un-aliased deploys — today's behavior, unchanged.
//  2. Deploys an alias pinned until the idle sweep unpinned them (item 7).
//     They are ordinary un-aliased deploys by the time this query runs.
//  3. Superseded deploys beyond the project's rollback retention (item 8).
//
// A deploy an alias still points at appears in none of them.
const reclaimableSQL = `
WITH ranked AS (
    SELECT id,
           row_number() OVER (PARTITION BY project_id ORDER BY created_at DESC) AS rank
    FROM deploys
    WHERE status = 'running' AND project_id IS NOT NULL
)
SELECT id, user_id, container_id
FROM deploys
WHERE status = 'running'
  AND NOT ` + aliasPinned + `
  AND (
        ttl_expires_at < ?
        OR (? > 0 AND id IN (SELECT id FROM ranked WHERE rank > ?))
      )
ORDER BY ttl_expires_at IS NULL, ttl_expires_at
LIMIT ?`

// FindExpiredWithDetails returns the deploys the watchdog should stop: TTL
// expiry plus alias-aware retention cases.
func (r *Repository) FindExpiredWithDetails(ctx context.Context, limit int) ([]ExpiredDeploy, error) {
	now := time.Now()

	rows, err := r.db.QueryContext(ctx, reclaimableSQL,
		controldb.FormatTime(now), r.gc.KeepPerProject, r.gc.KeepPerProject, limit)
	if err != nil {
		return nil, fmt.Errorf("find expired deploys with details: %w", err)
	}
	defer rows.Close()

	var deploys []ExpiredDeploy
	for rows.Next() {
		var d ExpiredDeploy
		if err := rows.Scan(&d.ID, &d.UserID, &d.ContainerID); err != nil {
			return nil, fmt.Errorf("scan expired deploy: %w", err)
		}
		deploys = append(deploys, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired deploys: %w", err)
	}

	return deploys, nil
}
