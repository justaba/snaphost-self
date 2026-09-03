// Package wiring adapts component-specific interfaces to the concrete
// implementations assembled by the application. Adapters keep orchestration
// policy (for example retry classification) out of repositories and workers.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"database/sql"

	"github.com/google/uuid"
	"go.uber.org/zap"

	builderevents "snaphost/internal/builder/events"
	builderlogs "snaphost/internal/builder/logs"
	"snaphost/internal/builder/scheduler"
	"snaphost/internal/buildevents"
	"snaphost/internal/control/apikey"
	"snaphost/internal/control/auth"
	"snaphost/internal/control/deploy"
	controllogs "snaphost/internal/control/logs"
	"snaphost/internal/control/saga"
	"snaphost/internal/httpapi/middleware"
	"snaphost/internal/logbus"
	"snaphost/internal/runtime/deployments"
	runtimelogs "snaphost/internal/runtime/logs"
	"snaphost/internal/runtime/runner"
)

// ---------------------------------------------------------------------------
// saga → build pipeline
// ---------------------------------------------------------------------------

// BuildScheduler satisfies saga.BuildScheduler by enqueueing directly.
type BuildScheduler struct {
	Scheduler *scheduler.Scheduler
}

// EnqueueBuild validates and queues a build. Request failures are translated
// into the retry semantics the saga needs.
func (c *BuildScheduler) EnqueueBuild(ctx context.Context, req saga.BuildRequest) error {
	_, err := c.Scheduler.Enqueue(ctx, scheduler.BuildRequest{
		DeployID:     req.DeployID,
		UserID:       req.UserID,
		SourceType:   req.SourceType,
		RepoURL:      req.RepoURL,
		Branch:       req.Branch,
		UploadID:     req.UploadID,
		CredentialID: req.CredentialID,
	})
	if err != nil {
		var reqErr *scheduler.RequestError
		if errors.As(err, &reqErr) {
			return &saga.OperationError{
				Code:      reqErr.Code,
				Message:   reqErr.Message,
				Retryable: reqErr.Retryable,
			}
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// saga → container runtime
// ---------------------------------------------------------------------------

// ContainerRuntime satisfies saga.Runtime through direct service calls.
type ContainerRuntime struct {
	Service *runner.Service
}

// Deploy starts the container and translates runtime errors into saga retry
// semantics without routing them through HTTP status codes.
func (c *ContainerRuntime) Deploy(ctx context.Context, req saga.DeployRequest) (*saga.DeployResponse, error) {
	result, err := c.Service.Deploy(ctx, runner.DeployRequest{
		DeployID:   req.DeployID,
		UserID:     req.UserID,
		ImageRef:   req.ImageRef,
		Port:       req.Port,
		TTLMinutes: req.TTLMinutes,
	})
	if err != nil {
		return nil, runtimeOperationError(err)
	}
	return &saga.DeployResponse{
		ContainerID: result.ContainerID,
		EndpointURL: result.EndpointURL,
	}, nil
}

func runtimeOperationError(err error) error {
	var probeErr *runner.ProbeError
	var validationErr *runner.ValidationError
	switch {
	case errors.As(err, &probeErr):
		return &saga.OperationError{Code: "probe_failed", Message: probeErr.Error()}
	case errors.As(err, &validationErr):
		return &saga.OperationError{Code: "validation_failed", Message: validationErr.Error()}
	case errors.Is(err, runner.ErrAlreadyRunning):
		return &saga.OperationError{Code: "already_running", Message: err.Error()}
	}
	return &saga.OperationError{Code: "deploy_failed", Message: err.Error(), Retryable: true}
}

// Stop tears the runtime down. A deploy the runtime does not know about is not
// an error because compensation must be safe to run twice.
func (c *ContainerRuntime) Stop(ctx context.Context, deployID, containerID string) error {
	err := c.Service.Undeploy(ctx, deployID, containerID)
	if err == nil {
		return nil
	}
	var validationErr *runner.ValidationError
	if errors.As(err, &validationErr) {
		return nil
	}
	return err
}

// StopStrict tears the runtime down and reports every refusal, including the
// ownership-validation ones Stop swallows.
//
// Stop's swallow is correct for compensation, which must be safe to run twice
// and treats "the runtime does not recognise this deploy" as already-done.
// There is one caller for which that answer is unsafe: project deletion, which
// hard-deletes the rows naming the container immediately afterwards. If the
// runtime refused because the stored container id did not match, the container
// is still there and the only record of it is about to be destroyed.
//
// A container that is genuinely absent still succeeds — the backend treats
// not-found as done — so this is stricter about refusals, not about outcomes.
func (c *ContainerRuntime) StopStrict(ctx context.Context, deployID, containerID string) error {
	return c.Service.Undeploy(ctx, deployID, containerID)
}

// RemoveImage releases a deploy artifact that has no container to stop, such
// as a failed build or a deploy already stopped before image GC was enabled.
func (c *ContainerRuntime) RemoveImage(ctx context.Context, deployID, imageRef string) error {
	return c.Service.RemoveImage(ctx, deployID, imageRef)
}

// ---------------------------------------------------------------------------
// builder → control
// ---------------------------------------------------------------------------

// StatusReporter satisfies pipeline.StatusReporter by writing to the deploy
// repository. It takes string ids because that is what the build pipeline
// carries; a malformed one is a programming error, not a transient failure.
type StatusReporter struct {
	Repo *deploy.Repository
}

func (r *StatusReporter) ReportBuilding(ctx context.Context, deployID string) error {
	return r.updateStatus(ctx, deployID, "building", nil)
}

// ReportImageLoaded records the artifact on the deploy row the moment it
// exists in the daemon, so the image sweep can name it even if everything
// after the build rejects the deploy.
func (r *StatusReporter) ReportImageLoaded(ctx context.Context, deployID, imageRef string) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return r.Repo.SetImageRef(ctx, id, imageRef)
}

func (r *StatusReporter) ReportFailed(ctx context.Context, deployID, reason string) error {
	return r.updateStatus(ctx, deployID, "failed", &reason)
}

func (r *StatusReporter) updateStatus(ctx context.Context, deployID, status string, reason *string) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return r.Repo.UpdateStatus(ctx, id, status, reason)
}

// ---------------------------------------------------------------------------
// runtime → control
// ---------------------------------------------------------------------------

// DeploymentStore exposes the control-plane repository through the narrow
// interfaces required by the runtime and its cleanup loop.
type DeploymentStore struct {
	Repo *deploy.Repository
}

func (c *DeploymentStore) UpdateDeployStatus(ctx context.Context, deployID string, status string, failureReason *string) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return c.Repo.UpdateStatus(ctx, id, status, failureReason)
}

func (c *DeploymentStore) SetDeployRunning(ctx context.Context, deployID string, req deployments.SetRunningRequest) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return c.Repo.SetRunning(ctx, id, req.ImageRef, req.EndpointURL, req.Subdomain, req.ContainerID, req.TTLExpiresAt)
}

func (c *DeploymentStore) MarkDeployImageDeleted(ctx context.Context, deployID string) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return c.Repo.MarkImageDeleted(ctx, id)
}

// GetDeploy returns the stored deploy. A missing row is deployments.ErrNotFound
// so ownership checks distinguish "unknown deploy" from a failed lookup.
func (c *DeploymentStore) GetDeploy(ctx context.Context, deployID string) (*deployments.Info, error) {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return nil, deployments.ErrNotFound
	}
	d, err := c.Repo.Get(ctx, id)
	if err != nil {
		// The repository wraps sql.ErrNoRows rather than exporting a sentinel
		// of its own, so that is what "no such deploy" looks like here.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, deployments.ErrNotFound
		}
		return nil, err
	}
	info := &deployments.Info{
		DeployID: d.ID.String(),
		UserID:   d.UserID.String(),
		Status:   d.Status,
	}
	if d.ImageRef != nil {
		info.ImageRef = *d.ImageRef
	}
	if d.ContainerID != nil {
		info.ContainerID = *d.ContainerID
	}
	return info, nil
}

// ListExpiredDeploys is what the watchdog sweeps. The repository decides what
// counts as expired — un-aliased past TTL or beyond per-project retention —
// and an aliased deploy is never returned.
func (c *DeploymentStore) ListExpiredDeploys(ctx context.Context, limit int) ([]deployments.Expired, error) {
	rows, err := c.Repo.FindExpiredWithDetails(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]deployments.Expired, 0, len(rows))
	for _, r := range rows {
		e := deployments.Expired{ID: r.ID.String(), UserID: r.UserID.String()}
		if r.ContainerID != nil {
			e.ContainerID = *r.ContainerID
		}
		out = append(out, e)
	}
	return out, nil
}

func (c *DeploymentStore) ListImagesPendingCleanup(ctx context.Context, limit int) ([]deployments.ImageCleanup, error) {
	rows, err := c.Repo.FindImagesPendingCleanup(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]deployments.ImageCleanup, 0, len(rows))
	for _, row := range rows {
		out = append(out, deployments.ImageCleanup{ID: row.ID.String(), ImageRef: row.ImageRef})
	}
	return out, nil
}

func (c *DeploymentStore) ReclaimStoppedDeploys(ctx context.Context, limit int) (int, error) {
	return c.Repo.ReclaimStoppedDeploys(ctx, limit)
}

// ---------------------------------------------------------------------------
// HTTP authentication → control
// ---------------------------------------------------------------------------

// SessionVerifier satisfies middleware.SessionVerifier by reading the session
// table directly.
//
// This is the browser's whole authentication path. It replaced a JWKS fetched
// over the internet from Supabase and cached for an hour, which meant a panel
// nobody could log into whenever that host was unreachable — and a signature
// check whose trust root was a service this platform exists not to need.
type SessionVerifier struct {
	Service *auth.Service
}

func (v *SessionVerifier) Verify(ctx context.Context, token string) (middleware.Identity, error) {
	session, err := v.Service.Verify(ctx, token)
	if err != nil {
		return middleware.Identity{}, err
	}
	return middleware.Identity{
		UserID: session.UserID.String(),
		Email:  session.Email,
		Role:   session.Role,
	}, nil
}

// DeployOwnership satisfies wslogs.DeployOwner. The log stream authorises the
// subscriber against the deploy's owner, which needs a repository lookup
// no repository of its own for.
type DeployOwnership struct {
	Repo *deploy.Repository
}

func (o *DeployOwnership) Owner(ctx context.Context, deployID uuid.UUID) (string, error) {
	d, err := o.Repo.Get(ctx, deployID)
	if err != nil {
		return "", err
	}
	return d.UserID.String(), nil
}

// KeyVerifier satisfies middleware.KeyVerifier by hashing the presented key
// and looking it up directly.
//
// This is the path every non-browser client authenticates on — the MCP server,
// the editor extension, any `sk_` bearer — so it must behave exactly as the
// HTTP verify endpoint did: an unknown or revoked key is an error, and the
// last-used timestamp is best-effort and never blocks the request.
type KeyVerifier struct {
	Repo *apikey.Repository
	Log  *zap.Logger
}

func (v *KeyVerifier) Verify(ctx context.Context, key string) (string, error) {
	if !apikey.HasKeyPrefix(key) {
		return "", errors.New("not an api key")
	}
	userID, keyID, err := v.Repo.VerifyByHash(ctx, apikey.Hash(key))
	if err != nil {
		return "", err
	}
	go func(id uuid.UUID) {
		touchCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := v.Repo.TouchLastUsed(touchCtx, id); err != nil {
			v.Log.Warn("failed to record api key use", zap.Error(err))
		}
	}(keyID)
	return userID.String(), nil
}

// ---------------------------------------------------------------------------
// everything → the log bus
// ---------------------------------------------------------------------------

// Each component declares the log shape it consumes and gets a narrow adapter
// onto internal/logbus. This avoids making components depend on the bus itself.

// ControlLogPublisher satisfies logs.Publisher for the saga's progress lines.
type ControlLogPublisher struct{ Bus *logbus.Bus }

func (p *ControlLogPublisher) Publish(deployID string, line controllogs.LogLine) error {
	p.Bus.Publish(deployID, logbus.Line{
		Stage: line.Stage, Text: line.Text, Level: line.Level, Timestamp: line.Timestamp,
	})
	return nil
}

func (p *ControlLogPublisher) Close() error { return nil }

// BuilderLogPublisher satisfies the build pipeline's logs.Publisher.
type BuilderLogPublisher struct{ Bus *logbus.Bus }

func (p *BuilderLogPublisher) Publish(deployID string, line builderlogs.LogLine) error {
	p.Bus.Publish(deployID, logbus.Line{
		Stage: line.Stage, Text: line.Text, Level: line.Level, Timestamp: line.Timestamp,
	})
	return nil
}

func (p *BuilderLogPublisher) Close() error { return nil }

// RuntimeLogPublisher satisfies the runtime's logs.Publisher.
type RuntimeLogPublisher struct{ Bus *logbus.Bus }

func (p *RuntimeLogPublisher) Publish(deployID string, line runtimelogs.LogLine) error {
	p.Bus.Publish(deployID, logbus.Line{
		Stage: line.Stage, Text: line.Text, Level: line.Level, Timestamp: line.Timestamp,
	})
	return nil
}

func (p *RuntimeLogPublisher) Close() error { return nil }

// LogArchiver satisfies deploy.LogArchiver: it hands the repository the tail of
// what is still in memory when a deploy fails, encoded as an opaque blob.
//
// ArchiveLines bounds what is stored. The bus retains 1000 lines per deploy and
// this keeps the last 200 of them, because the useful part of a failed build's
// output is its end — the error and what led to it — and the column sits in a
// row the admin console lists.
type LogArchiver struct {
	Bus   *logbus.Bus
	Lines int
}

func (a *LogArchiver) Archive(deployID string) []byte {
	n := a.Lines
	if n <= 0 {
		n = 200
	}
	return logbus.EncodeLines(a.Bus.Tail(deployID, n))
}

// ---------------------------------------------------------------------------
// builder ↔ saga: build outcomes
// ---------------------------------------------------------------------------

// BuildEventPublisher satisfies events.Publisher by putting the event on the
// in-process bus instead of a Redis channel.
type BuildEventPublisher struct{ Bus *buildevents.Bus }

func (p *BuildEventPublisher) Publish(_ context.Context, event builderevents.BuildEvent) error {
	p.Bus.Publish(buildevents.Event{
		Type:      buildevents.Type(event.Type),
		DeployID:  event.DeployID,
		ImageRef:  event.ImageRef,
		Port:      event.Port,
		CommitSHA: event.CommitSHA,
		Reason:    event.Reason,
		Timestamp: event.Timestamp,
	})
	return nil
}

// BuildEventWaiter satisfies saga.BuildEvents.
//
// The two sides keep their own types rather than sharing one. That is not
// ceremony: the saga deliberately did not import the builder's package even
// when both were separate modules, and collapsing them now would tie the state
// machine to the pipeline's wire format for no gain.
type BuildEventWaiter struct{ Bus *buildevents.Bus }

func (w *BuildEventWaiter) Wait(ctx context.Context, deployID string, timeout time.Duration) (saga.BuildOutcome, error) {
	ev, err := w.Bus.Wait(ctx, deployID, timeout)
	if err != nil {
		return saga.BuildOutcome{}, err
	}
	return saga.BuildOutcome{
		Failed:    ev.Type == buildevents.Failed,
		ImageRef:  ev.ImageRef,
		Port:      ev.Port,
		CommitSHA: ev.CommitSHA,
		Reason:    ev.Reason,
	}, nil
}
