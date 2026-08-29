// Package wiring holds the adapters that let the services call each other
// directly instead of over HTTP.
//
// The seams themselves are not new: every one of these was already an
// interface, because the services were separate processes and had to be
// mockable in tests. What changes is the implementation behind them — an
// in-process call rather than a JSON round trip to localhost.
//
// Two things are deliberately preserved rather than simplified away:
//
//   - The error classification. The saga distinguishes "this will never work"
//     from "try again" by HTTP status, and refunding versus requeueing hangs on
//     it. Each adapter reconstructs the exact status its HTTP handler would
//     have returned, so the decision is made on the same evidence as before.
//   - The validation. Nothing here re-implements a check that a handler did;
//     the adapters call the same functions the handlers call.
//
// What does disappear is the internal X-Webhook-Secret layer. A shared secret
// between two goroutines in one process protects nothing.
package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"database/sql"

	"github.com/google/uuid"
	"go.uber.org/zap"

	aillm "snaphost/internal/ai/llm"
	aiservice "snaphost/internal/ai/service"
	builderai "snaphost/internal/builder/ai"
	builderapi "snaphost/internal/builder/api"
	builderevents "snaphost/internal/builder/events"
	builderlogs "snaphost/internal/builder/logs"
	"snaphost/internal/buildevents"
	"snaphost/internal/control/apikey"
	"snaphost/internal/control/auth"
	"snaphost/internal/control/deploy"
	controllogs "snaphost/internal/control/logs"
	"snaphost/internal/control/saga"
	"snaphost/internal/gateway/middleware"
	"snaphost/internal/logbus"
	"snaphost/internal/runtime/billing"
	runtimelogs "snaphost/internal/runtime/logs"
	"snaphost/internal/runtime/runner"
)

// ---------------------------------------------------------------------------
// saga → builder
// ---------------------------------------------------------------------------

// BuilderClient satisfies saga.BuilderClient by enqueueing directly.
type BuilderClient struct {
	Enqueuer *builderapi.Enqueuer
}

// EnqueueBuild validates and queues the build. A rejected request becomes a
// *saga.StatusError carrying the status the API would have answered with, so
// the orchestrator's terminal-versus-transient decision is unchanged — a 400
// compensates, a 429 is retried.
func (c *BuilderClient) EnqueueBuild(ctx context.Context, req saga.BuildRequest) error {
	_, err := c.Enqueuer.Enqueue(ctx, builderapi.BuildRequest{
		DeployID:     req.DeployID,
		UserID:       req.UserID,
		SourceType:   req.SourceType,
		RepoURL:      req.RepoURL,
		Branch:       req.Branch,
		UploadID:     req.UploadID,
		CredentialID: req.CredentialID,
	})
	if err != nil {
		var reqErr *builderapi.RequestError
		if errors.As(err, &reqErr) {
			return &saga.StatusError{
				StatusCode: reqErr.Status,
				Code:       reqErr.Code,
				Message:    reqErr.Message,
			}
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// saga → runtime
// ---------------------------------------------------------------------------

// RunnerClient satisfies saga.RunnerClient by calling the runtime service.
type RunnerClient struct {
	Service *runner.Service
}

// Deploy starts the container. The error mapping mirrors the runtime's HTTP
// handler exactly, because the saga reads the status to decide whether to
// refund: a failed liveness probe is 422 and terminal, a validation failure is
// 400 and terminal, an already-running deploy is 409, and anything else is 500
// and retried.
func (c *RunnerClient) Deploy(ctx context.Context, req saga.DeployRequest) (*saga.DeployResponse, error) {
	result, err := c.Service.Deploy(ctx, runner.DeployRequest{
		DeployID:   req.DeployID,
		UserID:     req.UserID,
		ImageRef:   req.ImageRef,
		Port:       req.Port,
		TTLMinutes: req.TTLMinutes,
	})
	if err != nil {
		return nil, runnerStatusError(err)
	}
	return &saga.DeployResponse{
		ContainerID: result.ContainerID,
		EndpointURL: result.EndpointURL,
	}, nil
}

func runnerStatusError(err error) error {
	var probeErr *runner.ProbeError
	var validationErr *runner.ValidationError
	switch {
	case errors.As(err, &probeErr):
		return &saga.StatusError{StatusCode: http.StatusUnprocessableEntity, Code: "probe_failed", Message: probeErr.Error()}
	case errors.As(err, &validationErr):
		return &saga.StatusError{StatusCode: http.StatusBadRequest, Code: "validation_failed", Message: validationErr.Error()}
	case errors.Is(err, runner.ErrAlreadyRunning):
		return &saga.StatusError{StatusCode: http.StatusConflict, Code: "already_running", Message: err.Error()}
	}
	return &saga.StatusError{StatusCode: http.StatusInternalServerError, Code: "deploy_failed", Message: err.Error()}
}

// Stop tears the runtime down. A deploy the runtime does not know about is not
// an error: the HTTP path treated 404 as success, because compensation must be
// safe to run twice.
func (c *RunnerClient) Stop(ctx context.Context, deployID, containerID string) error {
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
// builder → ai
// ---------------------------------------------------------------------------

// AIClient satisfies builder/ai.Client by calling the generator directly.
type AIClient struct {
	Service *aiservice.Service
}

// GenerateDockerfile converts between the two request shapes through JSON.
//
// The builder's ProjectInfo is an interface{} that used to be serialised onto
// the wire and decoded into the generator's typed struct. Round-tripping it
// here does exactly what the HTTP hop did, which is the point: the conversion
// stays honest rather than becoming a hand-written field mapping that can
// silently disagree with the JSON tags.
func (c *AIClient) GenerateDockerfile(ctx context.Context, req builderai.GenerateRequest) (*builderai.GenerateResponse, error) {
	var projectInfo aillm.ProjectInfo
	if req.ProjectInfo != nil {
		raw, err := json.Marshal(req.ProjectInfo)
		if err != nil {
			return nil, fmt.Errorf("encode project info: %w", err)
		}
		if err := json.Unmarshal(raw, &projectInfo); err != nil {
			return nil, fmt.Errorf("decode project info: %w", err)
		}
	}

	resp, err := c.Service.GenerateDockerfile(ctx, aillm.GenerateRequest{
		DeployID:    req.DeployID,
		UserID:      req.UserID,
		ProjectInfo: projectInfo,
		FileTree:    req.FileTree,
		KeyFiles:    req.KeyFiles,
	})
	if err != nil {
		return nil, err
	}

	return &builderai.GenerateResponse{
		Dockerfile:   resp.Dockerfile,
		ExposePort:   resp.ExposePort,
		BuildArgs:    resp.BuildArgs,
		SupportFiles: resp.SupportFiles,
		CacheHit:     resp.CacheHit,
		Metadata: builderai.Metadata{
			Source:     resp.Metadata.Source,
			TemplateID: resp.Metadata.TemplateID,
			DurationMs: resp.Metadata.DurationMs,
			TokensUsed: resp.Metadata.TokensUsed,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// runtime → control
// ---------------------------------------------------------------------------

// BillingClient satisfies runner.BillingClient and the watchdog's lookup of
// expired deploys, reading and writing the deploy repository directly.
//
// The name is inherited from when this was an HTTP client to the billing
// service. There is no billing left; renaming it is a wider change than this
// step, and a wrong name is easier to see than a wrong wire.
type BillingClient struct {
	Repo *deploy.Repository
}

func (c *BillingClient) UpdateDeployStatus(ctx context.Context, deployID string, status string, failureReason *string) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return c.Repo.UpdateStatus(ctx, id, status, failureReason)
}

func (c *BillingClient) SetDeployRunning(ctx context.Context, deployID string, req billing.SetRunningRequest) error {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return fmt.Errorf("invalid deploy id %q: %w", deployID, err)
	}
	return c.Repo.SetRunning(ctx, id, req.ImageRef, req.EndpointURL, req.Subdomain, req.ContainerID, req.TTLExpiresAt)
}

// GetDeploy returns the stored deploy. A missing row is billing.ErrDeployNotFound
// so the runtime's ownership checks keep distinguishing "unknown deploy" from
// "lookup failed" — the first is a refusal, the second is worth retrying.
func (c *BillingClient) GetDeploy(ctx context.Context, deployID string) (*billing.DeployInfo, error) {
	id, err := uuid.Parse(deployID)
	if err != nil {
		return nil, billing.ErrDeployNotFound
	}
	d, err := c.Repo.Get(ctx, id)
	if err != nil {
		// The repository wraps sql.ErrNoRows rather than exporting a sentinel
		// of its own, so that is what "no such deploy" looks like here.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, billing.ErrDeployNotFound
		}
		return nil, err
	}
	info := &billing.DeployInfo{
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
// counts as expired — un-aliased past TTL, an alias idle too long, or a build
// beyond the per-project retention — and an aliased deploy is never returned.
func (c *BillingClient) ListExpiredDeploys(ctx context.Context, limit int) ([]billing.ExpiredDeploy, error) {
	rows, err := c.Repo.FindExpiredWithDetails(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]billing.ExpiredDeploy, 0, len(rows))
	for _, r := range rows {
		e := billing.ExpiredDeploy{ID: r.ID.String(), UserID: r.UserID.String()}
		if r.ContainerID != nil {
			e.ContainerID = *r.ContainerID
		}
		out = append(out, e)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// gateway → control
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
// subscriber against the deploy's owner, which needs a lookup the gateway has
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

// The control plane, the build pipeline and the runtime each declare their own
// LogLine and Publisher, identically, because they were three processes that
// met on a Redis channel. Rather than collapse the three into one type — a wide
// diff through every call site that publishes a log line — each keeps its
// interface and gets an adapter onto internal/logbus.
//
// None of them can fail any more. The error in the signature is what the Redis
// implementations returned nil for anyway: publishing a log line sits on the
// build pipeline's critical path, and a caller has nothing useful to do about
// it going wrong.

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
