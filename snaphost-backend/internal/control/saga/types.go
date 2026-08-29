// Package saga implements an asynchronous orchestrator for deploy lifecycle:
// reserve coins → enqueue build → wait for build → provision runtime →
// commit coins. Every state transition is persisted in deploy_sagas BEFORE
// the next remote call, which makes the orchestrator safe to resume after
// a process crash.
package saga

import "time"

// Step is the position in the saga state machine. Persisted in
// deploy_sagas.current_step.
type Step string

const (
	// StepPending — saga row exists but no work has begun.
	StepPending Step = "pending"
	// StepBuilding — build job has been enqueued to builder-svc.
	StepBuilding Step = "building"
	// StepBuilt — builder-svc has reported a successful build.
	StepBuilt Step = "built"
	// StepProvisioning — runner-svc has accepted the deploy and the
	// container is starting up.
	StepProvisioning Step = "provisioning"
	// StepRunning — terminal success: the container is up and answering.
	StepRunning Step = "running"
	// StepFailed — terminal failure: rollback completed.
	StepFailed Step = "failed"
	// StepCompensating — rollback in progress.
	StepCompensating Step = "compensating"
	// StepCompensated — rollback completed (terminal).
	StepCompensated Step = "compensated"
)

// SagaJob is the message body enqueued to the Redis Stream consumed by
// the saga worker. It carries everything the orchestrator needs to start
// or resume work — the orchestrator never trusts these fields when a row
// already exists in deploy_sagas, it always reads from the DB first.
type SagaJob struct {
	DeployID string `json:"deploy_id"`
	UserID   string `json:"user_id"`
	// SourceType is the deploy source (git_public | git_private | archive).
	// Empty means git_public — jobs enqueued before 14b-1 have no field.
	SourceType string `json:"source_type,omitempty"`
	RepoURL    string `json:"repo_url"`
	Branch     string `json:"branch"`
	UploadID   string `json:"upload_id,omitempty"`
	// CredentialID references the short-lived git credential in Redis
	// (gitcred:<id>) for git_private. Never the secret itself.
	CredentialID string    `json:"credential_id,omitempty"`
	EnqueuedAt   time.Time `json:"enqueued_at"`
}

// SagaState mirrors a row in deploy_sagas. Pointer fields are nullable
// in SQL.
type SagaState struct {
	DeployID string
	UserID   string
	// SourceType, UploadID, and CredentialID are mirrored from the deploy
	// at saga creation so the resume sweeper can rebuild a job without
	// the original stream message. CredentialID is an opaque Redis key
	// suffix — never the secret.
	SourceType       string
	UploadID         *string
	CredentialID     *string
	CurrentStep      Step
	ImageBuilt       bool
	ContainerRunning bool
	ImageRef         *string
	CommitSHA        *string
	// AppPort is the application listen port published by builder-svc
	// in the build event. Nil means builder-svc could not determine one
	// (no EXPOSE in Dockerfile and no language heuristic match).
	AppPort       *int
	ContainerID   *string
	EndpointURL   *string
	FailureReason *string
	RetryCount    int
}

// IsTerminal reports whether the saga has reached a state from which it
// will not progress: success (running) or one of the failure terminals.
func (s SagaState) IsTerminal() bool {
	switch s.CurrentStep {
	case StepRunning, StepFailed, StepCompensated:
		return true
	}
	return false
}
