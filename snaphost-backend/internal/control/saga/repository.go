package saga

import (
	"context"
	"errors"
	"fmt"
	"time"

	"database/sql"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

// ErrSagaNotFound is returned when no row exists in deploy_sagas for the
// requested deploy_id.
var ErrSagaNotFound = errors.New("saga not found")

// Repository provides data access for the deploy_sagas table. Each
// mutation is expressed as a small focused method so callers can read
// the saga code as a sequence of intent-level actions.
type Repository struct {
	db *sql.DB
}

// NewRepository constructs a Repository over the given connection pool.
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

// Create inserts a new saga row in the pending step. Idempotent: if a
// row already exists for the given deploy_id, no change is made. An
// empty sourceType is normalized to git_public; an empty uploadID is
// stored as NULL.
func (r *Repository) Create(ctx context.Context, deployID, userID uuid.UUID, sourceType, uploadID, credentialID string) error {
	if sourceType == "" {
		sourceType = "git_public"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, source_type, upload_id, credential_id)
		 VALUES (?, ?, 'pending', ?, nullif(?, ''), nullif(?, ''))
		 ON CONFLICT (deploy_id) DO NOTHING`,
		deployID.String(), userID.String(), sourceType, uploadID, credentialID,
	)
	if err != nil {
		return fmt.Errorf("create saga: %w", err)
	}
	return nil
}

// Get loads a saga state by deploy_id. Returns ErrSagaNotFound if the
// row does not exist.
func (r *Repository) Get(ctx context.Context, deployID uuid.UUID) (*SagaState, error) {
	s := &SagaState{}
	var (
		stepStr       string
		imageRef      *string
		commitSHA     *string
		appPort       *int
		containerID   *string
		endpointURL   *string
		failureReason *string
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT deploy_id, user_id, current_step, source_type, upload_id, credential_id,
		        image_built, container_running,
		        image_ref, commit_sha, app_port,
		        container_id, endpoint_url,
		        failure_reason, retry_count
		 FROM deploy_sagas WHERE deploy_id = ?`,
		deployID.String(),
	).Scan(
		&s.DeployID, &s.UserID, &stepStr, &s.SourceType, &s.UploadID, &s.CredentialID,
		&s.ImageBuilt, &s.ContainerRunning,
		&imageRef, &commitSHA, &appPort,
		&containerID, &endpointURL,
		&failureReason, &s.RetryCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSagaNotFound
		}
		return nil, fmt.Errorf("get saga: %w", err)
	}
	s.CurrentStep = Step(stepStr)
	s.ImageRef = imageRef
	s.CommitSHA = commitSHA
	s.AppPort = appPort
	s.ContainerID = containerID
	s.EndpointURL = endpointURL
	s.FailureReason = failureReason
	return s, nil
}

// UpdateStep advances the state machine. The trigger on deploy_sagas
// keeps updated_at fresh; started_at and completed_at are managed here.
func (r *Repository) UpdateStep(ctx context.Context, deployID uuid.UUID, step Step) error {
	// The step is bound three times and the timestamp twice: SQLite's
	// placeholders are positional, so each use needs its own.
	now := controldb.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas
		 SET current_step = ?,
		     started_at   = COALESCE(started_at, CASE WHEN ? <> 'pending' THEN ? END),
		     completed_at = CASE WHEN ? IN ('running','failed','compensated') THEN ? ELSE completed_at END
		 WHERE deploy_id = ?`,
		string(step), string(step), now, string(step), now, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("update saga step: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update saga step: %w", err)
	}
	if affected == 0 {
		return ErrSagaNotFound
	}
	return nil
}

// MarkImageBuilt records the build output on both durable views.
//
// The image reference goes to deploys as well as to the saga, and that is the
// point of the transaction. The saga row is orchestration state; deploys.image_ref
// is what the image sweep reads, and it used to be written only by SetRunning
// — so a build that succeeded and then failed to start left an image on the
// host with nothing in the swept table naming it. The eager removal on the
// probe-failure path covered the common case and nothing covered the rest: a
// container that could not be created, a compensating saga, or an eager
// removal that itself failed. Those images were unreclaimable for the life of
// the installation.
//
// Writing it here means the artifact is nameable from the moment it exists,
// whatever happens next.
func (r *Repository) MarkImageBuilt(ctx context.Context, deployID uuid.UUID, imageRef, commitSHA string, port int) error {
	var portArg any
	if port > 0 {
		portArg = port
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark image built: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE deploy_sagas
		    SET image_built = 1,
		        image_ref   = ?,
		        commit_sha  = ?,
		        app_port    = COALESCE(?, app_port)
		  WHERE deploy_id = ?`,
		imageRef, commitSHA, portArg, deployID.String(),
	); err != nil {
		return fmt.Errorf("mark image built: %w", err)
	}

	// commit_sha rides along because the build is where it becomes known, and
	// a deploy that never started should still say what it was built from.
	// image_deleted_at is cleared: this is a freshly built artifact, and a
	// stale marker from a reused row would hide it from the sweep.
	if _, err := tx.ExecContext(ctx,
		`UPDATE deploys
		    SET image_ref = ?,
		        commit_sha = COALESCE(NULLIF(?, ''), commit_sha),
		        image_deleted_at = NULL,
		        updated_at = ?
		  WHERE id = ?`,
		imageRef, commitSHA, controldb.Now(), deployID.String(),
	); err != nil {
		return fmt.Errorf("record built image on deploy: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark image built: %w", err)
	}
	return nil
}

// MarkContainerRunning records the container runtime details.
func (r *Repository) MarkContainerRunning(ctx context.Context, deployID uuid.UUID, containerID, endpointURL string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas SET container_running = 1, container_id = ?, endpoint_url = ? WHERE deploy_id = ?`,
		containerID, endpointURL, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("mark container running: %w", err)
	}
	return nil
}

// MarkFailed transitions the saga into the failed terminal state and
// records the user-visible reason.
func (r *Repository) MarkFailed(ctx context.Context, deployID uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas
		 SET current_step = 'failed', failure_reason = ?, completed_at = ?
		 WHERE deploy_id = ?`,
		reason, controldb.Now(), deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("mark saga failed: %w", err)
	}
	return nil
}

// MarkCompensated transitions the saga into the compensated terminal state.
func (r *Repository) MarkCompensated(ctx context.Context, deployID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas
		 SET current_step = 'compensated', completed_at = ?
		 WHERE deploy_id = ?`,
		controldb.Now(), deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("mark saga compensated: %w", err)
	}
	return nil
}

// IncrementRetry bumps retry_count and stores the most recent transient
// error for operator visibility. Does not move the state machine.
func (r *Repository) IncrementRetry(ctx context.Context, deployID uuid.UUID, lastError string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas SET retry_count = retry_count + 1, last_error = ? WHERE deploy_id = ?`,
		lastError, deployID.String(),
	)
	if err != nil {
		return fmt.Errorf("increment saga retry: %w", err)
	}
	return nil
}

// ListInFlight returns saga rows that have been stuck in a non-terminal
// state for at least 5 minutes. The resume sweeper enqueues synthetic
// jobs for each one so the orchestrator can pick up where it left off.
func (r *Repository) ListInFlight(ctx context.Context, limit int) ([]SagaState, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.deploy_id, s.user_id, s.current_step, s.source_type, s.upload_id, s.credential_id,
		        s.image_built, s.container_running,
		        s.image_ref, s.commit_sha, s.app_port,
		        s.container_id, s.endpoint_url,
		        s.failure_reason, s.retry_count
		 FROM deploy_sagas s
		 WHERE s.current_step IN ('pending','building','built','provisioning','compensating')
		   AND s.updated_at < ?
		 ORDER BY s.updated_at
		 LIMIT ?`,
		// The cutoff is computed here rather than in SQL: SQLite's datetime()
		// produces 'YYYY-MM-DD HH:MM:SS', which does not compare correctly
		// against the RFC 3339 text this column holds.
		controldb.FormatTime(time.Now().Add(-5*time.Minute)), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list in-flight sagas: %w", err)
	}
	defer rows.Close()

	var out []SagaState
	for rows.Next() {
		var (
			s       SagaState
			stepStr string
		)
		if err := rows.Scan(
			&s.DeployID, &s.UserID, &stepStr, &s.SourceType, &s.UploadID, &s.CredentialID,
			&s.ImageBuilt, &s.ContainerRunning,
			&s.ImageRef, &s.CommitSHA, &s.AppPort,
			&s.ContainerID, &s.EndpointURL,
			&s.FailureReason, &s.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scan in-flight saga: %w", err)
		}
		s.CurrentStep = Step(stepStr)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate in-flight sagas: %w", err)
	}
	return out, nil
}

// RewindInterruptedBuilds puts sagas that were waiting on a build back to the
// step that enqueues one, and reports how many moved.
//
// It runs once at startup, and only for sagas with no image reference: one that
// has an image finished building, and its wait step recognises that from the
// row rather than from an event. Rewinding those would build the same source a
// second time.
func (r *Repository) RewindInterruptedBuilds(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE deploy_sagas
		    SET current_step = ?
		  WHERE current_step = ?
		    AND (image_ref IS NULL OR image_ref = '')`,
		StepPending, StepBuilding,
	)
	if err != nil {
		return 0, fmt.Errorf("rewind interrupted builds: %w", err)
	}
	moved, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return moved, nil
}
