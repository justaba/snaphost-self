package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSagaNotFound is returned when no row exists in deploy_sagas for the
// requested deploy_id.
var ErrSagaNotFound = errors.New("saga not found")

// Repository provides data access for the deploy_sagas table. Each
// mutation is expressed as a small focused method so callers can read
// the saga code as a sequence of intent-level actions.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository constructs a Repository over the given connection pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts a new saga row in the pending step. Idempotent: if a
// row already exists for the given deploy_id, no change is made. An
// empty sourceType is normalized to git_public; an empty uploadID is
// stored as NULL.
func (r *Repository) Create(ctx context.Context, deployID, userID uuid.UUID, sourceType, uploadID, credentialID string) error {
	if sourceType == "" {
		sourceType = "git_public"
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, source_type, upload_id, credential_id)
		 VALUES ($1, $2, 'pending', $3, nullif($4, ''), nullif($5, ''))
		 ON CONFLICT (deploy_id) DO NOTHING`,
		deployID, userID, sourceType, uploadID, credentialID,
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
		reservationTx *uuid.UUID
		imageRef      *string
		commitSHA     *string
		appPort       *int
		containerID   *string
		endpointURL   *string
		failureReason *string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT deploy_id::text, user_id::text, current_step, source_type, upload_id, credential_id,
		        coins_reserved, image_built, container_running, coins_committed,
		        reservation_tx_id, image_ref, commit_sha, app_port,
		        container_id, endpoint_url,
		        failure_reason, retry_count
		 FROM deploy_sagas WHERE deploy_id = $1`,
		deployID,
	).Scan(
		&s.DeployID, &s.UserID, &stepStr, &s.SourceType, &s.UploadID, &s.CredentialID,
		&s.CoinsReserved, &s.ImageBuilt, &s.ContainerRunning, &s.CoinsCommitted,
		&reservationTx, &imageRef, &commitSHA, &appPort,
		&containerID, &endpointURL,
		&failureReason, &s.RetryCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSagaNotFound
		}
		return nil, fmt.Errorf("get saga: %w", err)
	}
	s.CurrentStep = Step(stepStr)
	if reservationTx != nil {
		v := reservationTx.String()
		s.ReservationTxID = &v
	}
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
	tag, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas
		 SET current_step = $1,
		     started_at  = COALESCE(started_at, CASE WHEN $1 <> 'pending' THEN now() END),
		     completed_at = CASE WHEN $1 IN ('running','failed','compensated') THEN now() ELSE completed_at END
		 WHERE deploy_id = $2`,
		string(step), deployID,
	)
	if err != nil {
		return fmt.Errorf("update saga step: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSagaNotFound
	}
	return nil
}

// MarkCoinsReserved records that wallet.Reserve completed and stores the
// transaction id for later Commit/Refund calls.
func (r *Repository) MarkCoinsReserved(ctx context.Context, deployID uuid.UUID, txID string) error {
	parsed, err := uuid.Parse(txID)
	if err != nil {
		return fmt.Errorf("invalid tx id %q: %w", txID, err)
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE deploy_sagas SET coins_reserved = true, reservation_tx_id = $1 WHERE deploy_id = $2`,
		parsed, deployID,
	)
	if err != nil {
		return fmt.Errorf("mark coins reserved: %w", err)
	}
	return nil
}

// MarkImageBuilt records the build output. Pass port=0 when the build
// event did not include one — the saga will then fall back to its
// configured default at run-container time.
func (r *Repository) MarkImageBuilt(ctx context.Context, deployID uuid.UUID, imageRef, commitSHA string, port int) error {
	var portArg any
	if port > 0 {
		portArg = port
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas
		    SET image_built = true,
		        image_ref   = $1,
		        commit_sha  = $2,
		        app_port    = COALESCE($3, app_port)
		  WHERE deploy_id = $4`,
		imageRef, commitSHA, portArg, deployID,
	)
	if err != nil {
		return fmt.Errorf("mark image built: %w", err)
	}
	return nil
}

// MarkContainerRunning records the runtime details from runner-svc.
func (r *Repository) MarkContainerRunning(ctx context.Context, deployID uuid.UUID, containerID, endpointURL string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas SET container_running = true, container_id = $1, endpoint_url = $2 WHERE deploy_id = $3`,
		containerID, endpointURL, deployID,
	)
	if err != nil {
		return fmt.Errorf("mark container running: %w", err)
	}
	return nil
}

// MarkCoinsCommitted records that the reservation has been finalized.
func (r *Repository) MarkCoinsCommitted(ctx context.Context, deployID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas SET coins_committed = true WHERE deploy_id = $1`,
		deployID,
	)
	if err != nil {
		return fmt.Errorf("mark coins committed: %w", err)
	}
	return nil
}

// MarkFailed transitions the saga into the failed terminal state and
// records the user-visible reason.
func (r *Repository) MarkFailed(ctx context.Context, deployID uuid.UUID, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas
		 SET current_step = 'failed', failure_reason = $1, completed_at = now()
		 WHERE deploy_id = $2`,
		reason, deployID,
	)
	if err != nil {
		return fmt.Errorf("mark saga failed: %w", err)
	}
	return nil
}

// MarkCompensated transitions the saga into the compensated terminal state.
func (r *Repository) MarkCompensated(ctx context.Context, deployID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas
		 SET current_step = 'compensated', completed_at = now()
		 WHERE deploy_id = $1`,
		deployID,
	)
	if err != nil {
		return fmt.Errorf("mark saga compensated: %w", err)
	}
	return nil
}

// IncrementRetry bumps retry_count and stores the most recent transient
// error for operator visibility. Does not move the state machine.
func (r *Repository) IncrementRetry(ctx context.Context, deployID uuid.UUID, lastError string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deploy_sagas SET retry_count = retry_count + 1, last_error = $1 WHERE deploy_id = $2`,
		lastError, deployID,
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
	rows, err := r.pool.Query(ctx,
		`SELECT s.deploy_id::text, s.user_id::text, s.current_step, s.source_type, s.upload_id, s.credential_id,
		        s.coins_reserved, s.image_built, s.container_running, s.coins_committed,
		        s.reservation_tx_id, s.image_ref, s.commit_sha, s.app_port,
		        s.container_id, s.endpoint_url,
		        s.failure_reason, s.retry_count
		 FROM deploy_sagas s
		 WHERE s.current_step IN ('reserved','building','built','provisioning','compensating')
		   AND s.updated_at < now() - interval '5 minutes'
		 ORDER BY s.updated_at
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list in-flight sagas: %w", err)
	}
	defer rows.Close()

	var out []SagaState
	for rows.Next() {
		var (
			s             SagaState
			stepStr       string
			reservationTx *uuid.UUID
		)
		if err := rows.Scan(
			&s.DeployID, &s.UserID, &stepStr, &s.SourceType, &s.UploadID, &s.CredentialID,
			&s.CoinsReserved, &s.ImageBuilt, &s.ContainerRunning, &s.CoinsCommitted,
			&reservationTx, &s.ImageRef, &s.CommitSHA, &s.AppPort,
			&s.ContainerID, &s.EndpointURL,
			&s.FailureReason, &s.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scan in-flight saga: %w", err)
		}
		s.CurrentStep = Step(stepStr)
		if reservationTx != nil {
			v := reservationTx.String()
			s.ReservationTxID = &v
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate in-flight sagas: %w", err)
	}
	return out, nil
}
