package saga

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/internal/control/logs"
)

// DeployRepository is the subset of the deploy repository the saga
// orchestrator depends on. Defined here (rather than imported from
// internal/deploy) to avoid an import cycle: deploy.Handler depends on
// the saga queue to enqueue jobs.
type DeployRepository interface {
	UpdateStatus(ctx context.Context, deployID uuid.UUID, status string, failureReason *string) error
	SetRunning(ctx context.Context, deployID uuid.UUID, imageRef, endpointURL, subdomain, containerID string, ttlExpiresAt time.Time) error
}

// Orchestrator drives a single saga from pending → running (or terminal
// failure). The state machine is read from deploy_sagas at every step,
// which is what makes Run safe to call repeatedly on the same saga.
type Orchestrator struct {
	Repo        *Repository
	DeployRepo  DeployRepository
	Builds      BuildScheduler
	Runtime     Runtime
	BuildEvents BuildEvents
	Publisher   logs.Publisher
	Log         *zap.Logger
	// BuildTimeout caps how long the saga waits on a build outcome
	// before giving up and compensating.
	BuildTimeout time.Duration
	// DeployPort is the container port the runtime reports to Traefik for
	// load-balancing. Must match the port the user app listens on.
	DeployPort int
	// DeployTTLMinutes is the TTL this deploy's tier grants, sent to
	// the runtime per deploy. Zero leaves the runtime on its own config
	// default, which is the pre-16 behavior.
	DeployTTLMinutes int
	// MaxTTLMinutes caps DeployTTLMinutes before work reaches the runtime.
	MaxTTLMinutes int
	// Aliases moves a project's verified custom domains onto the deploy that
	// just went live (Task 16a item 4). Optional — nil disables promotion,
	// which is the pre-16 behavior of publishing by hand.
	Aliases AliasPromoter
}

// AliasPromoter publishes a project's verified domains onto a deploy.
//
// Its own interface rather than a method on DeployRepository because the
// dependency is one way and one call deep: the saga knows a deploy went live,
// the domain layer knows what that means for aliases.
type AliasPromoter interface {
	PromoteToDeploy(ctx context.Context, deployID uuid.UUID) (int, error)
}

// ttlMinutes resolves the per-deploy TTL to send to the runtime.
func (o *Orchestrator) ttlMinutes() int {
	ttl := o.DeployTTLMinutes
	if o.MaxTTLMinutes > 0 && ttl > o.MaxTTLMinutes {
		ttl = o.MaxTTLMinutes
	}
	if ttl < 0 {
		return 0
	}
	return ttl
}

// BuildOutcome is the result the saga receives when a build ends.
type BuildOutcome struct {
	// Failed distinguishes the two terminal outcomes. Anything that is not a
	// failure carried an image.
	Failed    bool
	ImageRef  string
	Port      int
	CommitSHA string
	Reason    string
}

// BuildEvents reports the outcome of a build the saga is waiting on.
//
// The interface keeps the orchestrator independent of event storage and makes
// its state transitions directly testable.
type BuildEvents interface {
	// Wait blocks until the build for deployID ends, the timeout elapses, or
	// ctx is cancelled. A build that has already ended is answered without
	// waiting.
	Wait(ctx context.Context, deployID string, timeout time.Duration) (BuildOutcome, error)
}

// terminalError signals the orchestrator should abort and compensate.
// It wraps the user-visible reason for failure.
type terminalError struct{ reason string }

func (e *terminalError) Error() string { return e.reason }

func newTerminalError(format string, args ...any) error {
	return &terminalError{reason: fmt.Sprintf(format, args...)}
}

// Run executes the saga to a terminal state. Resumes from the persisted
// step if the saga row already exists.
func (o *Orchestrator) Run(ctx context.Context, job SagaJob) error {
	deployID, err := uuid.Parse(job.DeployID)
	if err != nil {
		return fmt.Errorf("orchestrator: invalid deploy id: %w", err)
	}
	userID, err := uuid.Parse(job.UserID)
	if err != nil {
		return fmt.Errorf("orchestrator: invalid user id: %w", err)
	}

	if err := o.Repo.Create(ctx, deployID, userID, job.SourceType, job.UploadID, job.CredentialID); err != nil {
		return err
	}

	for {
		state, err := o.Repo.Get(ctx, deployID)
		if err != nil {
			return err
		}
		if state.IsTerminal() {
			return nil
		}

		stepErr := o.runStep(ctx, job, deployID, userID, state)
		if stepErr == nil {
			continue
		}

		// Translate any error into either compensation or a transient
		// retry. terminalError → compensate; other errors are returned
		// so the queue retries the message.
		var tErr *terminalError
		if errors.As(stepErr, &tErr) {
			o.publish(deployID, "saga", "error", tErr.reason)
			if state.CurrentStep != StepCompensating {
				if err := o.Repo.UpdateStep(ctx, deployID, StepCompensating); err != nil {
					return err
				}
			}
			if err := o.compensate(ctx, deployID, tErr.reason); err != nil {
				o.Log.Error("compensation failed", zap.String("deploy_id", job.DeployID), zap.Error(err))
				return err
			}
			return nil
		}
		_ = o.Repo.IncrementRetry(ctx, deployID, stepErr.Error())
		return stepErr
	}
}

// runStep executes one transition based on the saga's current step. It
// always persists state changes BEFORE the next external call and AFTER
// the current call's side effect, so a crash never leaves the saga
// observably ahead of the database.
func (o *Orchestrator) runStep(ctx context.Context, job SagaJob, deployID, userID uuid.UUID, state *SagaState) error {
	switch state.CurrentStep {
	case StepPending:
		return o.stepEnqueueBuild(ctx, job, deployID, state)
	case StepBuilding:
		return o.stepWaitForBuild(ctx, deployID, state)
	case StepBuilt:
		return o.stepRunContainer(ctx, job, deployID, state)
	case StepProvisioning:
		return o.stepFinalize(ctx, deployID, state)
	default:
		return fmt.Errorf("unexpected step %q", state.CurrentStep)
	}
}

func (o *Orchestrator) stepEnqueueBuild(ctx context.Context, job SagaJob, deployID uuid.UUID, state *SagaState) error {
	o.publish(deployID, "saga", "info", "enqueueing build")
	// Source identity comes from the persisted saga row, not the job:
	// jobs synthesized by the resume sweeper carry only ids, and the
	// saga never trusts message fields when a DB row exists.
	sourceType := state.SourceType
	if sourceType == "" {
		sourceType = job.SourceType
	}
	uploadID := job.UploadID
	if state.UploadID != nil {
		uploadID = *state.UploadID
	}
	credentialID := job.CredentialID
	if state.CredentialID != nil {
		credentialID = *state.CredentialID
	}
	if err := o.Builds.EnqueueBuild(ctx, BuildRequest{
		DeployID:     job.DeployID,
		UserID:       job.UserID,
		SourceType:   sourceType,
		RepoURL:      job.RepoURL,
		Branch:       job.Branch,
		UploadID:     uploadID,
		CredentialID: credentialID,
	}); err != nil {
		var operationErr *OperationError
		if errors.As(err, &operationErr) && operationErr.Permanent() {
			return newTerminalError("%s", operationErr.UserReason())
		}
		return fmt.Errorf("enqueue build: %w", err)
	}
	if err := o.Repo.UpdateStep(ctx, deployID, StepBuilding); err != nil {
		return err
	}
	if err := o.DeployRepo.UpdateStatus(ctx, deployID, "building", nil); err != nil {
		return fmt.Errorf("update deploy status building: %w", err)
	}
	return nil
}

func (o *Orchestrator) stepWaitForBuild(ctx context.Context, deployID uuid.UUID, state *SagaState) error {
	// Race-safe pre-check: maybe the build pipeline finished and persisted before
	// we started subscribing.
	if state.ImageRef != nil && *state.ImageRef != "" {
		if err := o.Repo.UpdateStep(ctx, deployID, StepBuilt); err != nil {
			return err
		}
		return nil
	}

	o.publish(deployID, "saga", "info", "waiting for build to complete")
	event, err := o.BuildEvents.Wait(ctx, deployID.String(), o.BuildTimeout)
	if err != nil {
		return fmt.Errorf("wait for build event: %w", err)
	}
	if event.Failed {
		reason := event.Reason
		if reason == "" {
			reason = "build failed"
		}
		return newTerminalError("build failed: %s", reason)
	}
	if event.ImageRef == "" {
		return newTerminalError("build event missing image_ref")
	}
	if err := o.Repo.MarkImageBuilt(ctx, deployID, event.ImageRef, event.CommitSHA, event.Port); err != nil {
		return err
	}
	if err := o.Repo.UpdateStep(ctx, deployID, StepBuilt); err != nil {
		return err
	}
	o.publish(deployID, "saga", "info", "build complete")
	return nil
}

func (o *Orchestrator) stepRunContainer(ctx context.Context, job SagaJob, deployID uuid.UUID, state *SagaState) error {
	if state.ImageRef == nil {
		return newTerminalError("cannot run container: image_ref missing")
	}
	o.publish(deployID, "saga", "info", "starting container")
	// Port resolution priority: build-event (EXPOSE / language heuristic),
	// then env-wide DEPLOY_DEFAULT_PORT, then a hard-coded sane default.
	port := defaultDeployPort
	if o.DeployPort > 0 {
		port = o.DeployPort
	}
	if state.AppPort != nil && *state.AppPort > 0 {
		port = *state.AppPort
	}
	resp, err := o.Runtime.Deploy(ctx, DeployRequest{
		DeployID:   job.DeployID,
		UserID:     job.UserID,
		ImageRef:   *state.ImageRef,
		Port:       port,
		TTLMinutes: o.ttlMinutes(),
	})
	if err != nil {
		// A permanent runtime refusal means this deploy cannot work: the image
		// is rejected, or the container started and never answered on its port.
		// Retrying builds nothing new, so compensate — tear the runtime down —
		// instead of requeueing.
		var operationErr *OperationError
		if errors.As(err, &operationErr) && operationErr.Permanent() {
			return newTerminalError("%s", operationErr.UserReason())
		}
		return fmt.Errorf("runtime deploy: %w", err)
	}
	// The runtime persists subdomain, TTL and final status directly; the saga
	// mirrors enough into its own state to support resume-after-crash.
	if err := o.Repo.MarkContainerRunning(ctx, deployID, resp.ContainerID, resp.EndpointURL); err != nil {
		return err
	}
	if err := o.Repo.UpdateStep(ctx, deployID, StepProvisioning); err != nil {
		return err
	}
	return nil
}

// stepFinalize is the last transition: the container is up and has answered
// its probe, so the deploy becomes user-visibly running and the project's
// domains move onto it.
//
// This is the single point at which a build becomes the live version of a
// project.
func (o *Orchestrator) stepFinalize(ctx context.Context, deployID uuid.UUID, state *SagaState) error {
	if err := o.Repo.UpdateStep(ctx, deployID, StepRunning); err != nil {
		return err
	}
	if state.EndpointURL != nil && *state.EndpointURL != "" {
		o.publish(deployID, "saga", "info", "deployment ready at "+*state.EndpointURL)
	} else {
		o.publish(deployID, "saga", "info", "deployment ready")
	}

	o.promoteAliases(ctx, deployID)
	return nil
}

// promoteAliases publishes the project's verified domains onto this deploy.
//
// Deliberately best-effort and last: the deploy is already running, so a
// failure here must not tear it down. The domain keeps serving
// the previous build — a stale site, not a broken one — and the operator can
// move it by hand. The cost of treating it as fatal would be tearing down a
// working, paid-for deploy over a bookkeeping update.
//
// The same reasoning as Task 13a's log collection: an auxiliary step must not
// be able to break the primary path.
func (o *Orchestrator) promoteAliases(ctx context.Context, deployID uuid.UUID) {
	if o.Aliases == nil {
		return
	}
	moved, err := o.Aliases.PromoteToDeploy(ctx, deployID)
	if err != nil {
		o.Log.Warn("alias promotion failed; the domain still serves the previous deploy",
			zap.String("deploy_id", deployID.String()), zap.Error(err))
		o.publish(deployID, "saga", "warn",
			"could not repoint the custom domain automatically; the previous version is still served")
		return
	}
	if moved > 0 {
		o.Log.Info("custom domains repointed to the new deploy",
			zap.String("deploy_id", deployID.String()), zap.Int("domains", moved))
		o.publish(deployID, "saga", "info", "custom domain now serves this version")
	}
}

// compensate runs the rollback in reverse order of forward actions. It
// uses the persisted boolean flags to know what actually happened, so
// rerunning compensate is safe.
//
// A failure after the container started can leave an unreferenced runtime;
// compensation ensures it is stopped before the deploy is marked failed.
func (o *Orchestrator) compensate(ctx context.Context, deployID uuid.UUID, reason string) error {
	state, err := o.Repo.Get(ctx, deployID)
	if err != nil {
		return err
	}

	// Stop the container if it was started.
	if state.ContainerRunning && state.ContainerID != nil {
		o.publish(deployID, "saga", "info", "stopping container")
		if err := o.Runtime.Stop(ctx, deployID.String(), *state.ContainerID); err != nil {
			o.Log.Warn("compensation: stop container failed",
				zap.String("deploy_id", deployID.String()), zap.Error(err))
		}
	}

	if err := o.Repo.MarkFailed(ctx, deployID, reason); err != nil {
		return err
	}
	if err := o.Repo.MarkCompensated(ctx, deployID); err != nil {
		return err
	}
	if err := o.DeployRepo.UpdateStatus(ctx, deployID, "failed", &reason); err != nil {
		o.Log.Warn("compensation: update deploys.status failed",
			zap.String("deploy_id", deployID.String()), zap.Error(err))
	}
	o.publish(deployID, "saga", "error", "deployment failed: "+reason)
	return nil
}

// publish is a best-effort wrapper around the log publisher.
func (o *Orchestrator) publish(deployID uuid.UUID, stage, level, text string) {
	if o.Publisher == nil {
		return
	}
	if err := o.Publisher.Publish(deployID.String(), logs.LogLine{
		Stage:     stage,
		Level:     level,
		Text:      text,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		o.Log.Warn("publish saga log failed", zap.String("deploy_id", deployID.String()), zap.Error(err))
	}
}
