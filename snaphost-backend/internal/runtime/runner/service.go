// Package runner orchestrates container lifecycle operations and their durable
// deploy state.
package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/runtime/backend"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/deployments"
	"snaphost/internal/runtime/logs"
)

// DeploymentStore is the runtime's narrow view of durable deploy state.
type DeploymentStore interface {
	UpdateDeployStatus(ctx context.Context, deployID string, status string, failureReason *string) error
	SetDeployRunning(ctx context.Context, deployID string, req deployments.SetRunningRequest) error
	MarkDeployImageDeleted(ctx context.Context, deployID string) error
	GetDeploy(ctx context.Context, deployID string) (*deployments.Info, error)
}

// statusRunning indicates a deploy is already live; validateDeployRequest
// returns ErrAlreadyRunning to prevent a duplicate container.
const statusRunning = "running"

const (
	runningPersistenceTimeout = 10 * time.Second
	runningCleanupTimeout     = 30 * time.Second
)

// deployableStatuses lists statuses in which the saga is allowed to invoke
// the runtime for a deploy. Currently only "building" — the saga moves the deploy
// into this status at reservation time and calls the runtime concurrently with
// the build. After successful backend.Run, saga transitions through
// "provisioning" → "running".
//
// There is NO "built" intermediate status in the canonical state machine —
// see chk_deploys_status constraint in internal/control/db/migrations/0003_deploys.up.sql
// for the full allowed list.
var deployableStatuses = map[string]bool{
	"building": true,
	// A restart of a stopped deploy claims the row by moving it here before
	// it calls in, so the runtime sees 'provisioning' rather than 'stopped'.
	// The claim is what stops two clicks starting two containers for a row
	// that records one, and it means this check still refuses a deploy nobody
	// has claimed.
	"provisioning": true,
}

// DeployRequest is the input for Service.Deploy.
type DeployRequest struct {
	// DeployID is the deploy UUID.
	DeployID string
	// UserID is the owning user's UUID.
	UserID string
	// ImageRef is the full image reference produced by the build pipeline.
	ImageRef string
	// Env holds environment variables to inject into the container.
	Env map[string]string
	// Port is the port the user application listens on.
	Port int
	// TTLMinutes overrides the default TTL; 0 uses the config default.
	TTLMinutes int
}

// DeployResult is returned on successful deployment.
type DeployResult struct {
	// DeployID is the deploy UUID.
	DeployID string
	// EndpointURL is the public URL where the deploy is accessible.
	EndpointURL string
	// ContainerID is the backend-specific handle for the running container.
	ContainerID string
}

// Service orchestrates the backend and durable state for deploy operations.
type Service struct {
	backend   backend.Backend
	deploys   DeploymentStore
	publisher logs.Publisher
	cfg       *config.Config
	log       *zap.Logger
}

// NewService creates a new runner Service.
func NewService(b backend.Backend, deploys DeploymentStore, pub logs.Publisher, cfg *config.Config, log *zap.Logger) *Service {
	return &Service{
		backend:   b,
		deploys:   deploys,
		publisher: pub,
		cfg:       cfg,
		log:       log,
	}
}

// Deploy runs a container for the given deploy request. On success it persists
// the control-plane running state and starts a background log-forwarding
// goroutine. On failure it stores the failed status and reason.
func (s *Service) Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error) {
	s.publishLog(req.DeployID, "runtime-startup", "deploy accepted by runner")
	if err := s.validateDeployRequest(ctx, req); err != nil {
		s.publishLogLevel(req.DeployID, "runtime-startup", "image validation failed: "+userVisibleError(err), "error")
		return nil, err
	}
	s.publishLog(req.DeployID, "runtime-startup", "image validation passed")

	// Generate subdomain from the first 8 hex chars of the deploy ID.
	subdomain := generateSubdomain(req.DeployID)

	s.publishLog(req.DeployID, "runtime-startup", "preparing deployment")

	// Persist the provisioning transition.
	if err := s.deploys.UpdateDeployStatus(ctx, req.DeployID, "provisioning", nil); err != nil {
		s.log.Warn("failed to report provisioning status", zap.Error(err))
		// Non-fatal: continue with the deployment.
	}

	// Determine TTL.
	ttl := time.Duration(s.cfg.ContainerDefaultTTLMin) * time.Minute
	if req.TTLMinutes > 0 {
		ttl = time.Duration(req.TTLMinutes) * time.Minute
	}

	// Translate to backend request.
	backendReq := backend.RunRequest{
		DeployID:  req.DeployID,
		UserID:    req.UserID,
		ImageRef:  req.ImageRef,
		Env:       req.Env,
		Port:      req.Port,
		TTL:       ttl,
		Subdomain: subdomain,
	}

	// Run the container.
	s.publishLog(req.DeployID, "runtime-startup", "backend run started: "+s.backend.Name())
	result, err := s.backend.Run(ctx, backendReq)
	if err != nil {
		s.publishLogLevel(req.DeployID, "runtime-startup", "backend run failed: "+userVisibleError(err), "error")
		// Persist the failure.
		errMsg := err.Error()
		if stateErr := s.deploys.UpdateDeployStatus(ctx, req.DeployID, "failed", &errMsg); stateErr != nil {
			s.log.Error("failed to persist deploy failure", zap.Error(stateErr))
		}
		return nil, fmt.Errorf("backend run failed: %w", err)
	}

	// Liveness probe (Task 15b). Until this passes, "running" means "started",
	// which is not the same thing: on 2026-07-19 a deploy whose server was
	// bound to a fixed port reached running and answered every request with
	// UserCodeError. Nothing may report running before something answers on
	// the port we injected.
	if err := s.probeRuntime(ctx, req, result); err != nil {
		s.teardownAfterProbeFailure(ctx, req.DeployID, result.ContainerID, req.ImageRef)
		return nil, err
	}

	// Persist the successful side effect on a fresh bounded context. The client
	// can disconnect after the container starts; cancellation of that request
	// must not prevent the control plane from recording what now exists.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), runningPersistenceTimeout)
	err = s.deploys.SetDeployRunning(persistCtx, req.DeployID, deployments.SetRunningRequest{
		ImageRef:     req.ImageRef,
		EndpointURL:  result.EndpointURL,
		Subdomain:    subdomain,
		ContainerID:  result.ContainerID,
		TTLExpiresAt: result.TTLExpiresAt,
	})
	persistCancel()
	if err != nil {
		s.log.Error("failed to persist running deployment", zap.Error(err))
		s.publishLogLevel(req.DeployID, "runtime-startup",
			"could not persist the running deployment; stopping the uncommitted container", "error")
		persistErr := fmt.Errorf("persist running deployment: %w", err)
		if cleanupErr := s.rollbackUncommittedRuntime(req.DeployID, result.ContainerID); cleanupErr != nil {
			persistErr = errors.Join(persistErr, cleanupErr)
		}
		return nil, wrapTransient(persistErr)
	}

	s.publishLog(req.DeployID, "runtime-startup", "public URL ready: "+result.EndpointURL)

	// Start background log forwarder tied to a context that cancels when the
	// container stops or the TTL expires.
	logCtx, logCancel := context.WithTimeout(context.Background(), ttl+time.Minute)
	go func() {
		defer logCancel()
		s.StreamContainerLogs(logCtx, req.DeployID, result.ContainerID)
	}()

	s.log.Info("deploy succeeded",
		zap.String("deploy_id", req.DeployID),
		zap.String("endpoint", result.EndpointURL),
		zap.String("container_id", result.ContainerID),
	)

	return &DeployResult{
		DeployID:    req.DeployID,
		EndpointURL: result.EndpointURL,
		ContainerID: result.ContainerID,
	}, nil
}

// rollbackUncommittedRuntime reverses a container start whose durable running
// state could not be committed. It deliberately does not inherit the request
// context: by this point the side effect exists even if the caller has gone
// away, so cleanup has to get its own bounded opportunity to finish.
func (s *Service) rollbackUncommittedRuntime(deployID, containerID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), runningCleanupTimeout)
	defer cancel()

	if err := s.backend.Stop(cleanupCtx, deployID, containerID); err != nil {
		s.log.Error("failed to stop uncommitted container",
			zap.String("deploy_id", deployID),
			zap.String("container_id", containerID),
			zap.Error(err),
		)
		return fmt.Errorf("stop uncommitted container: %w", err)
	}

	// Strict validation only permits a fresh run from building. Reset the
	// transient provisioning marker after the container has actually gone.
	if err := s.deploys.UpdateDeployStatus(cleanupCtx, deployID, "building", nil); err != nil {
		s.log.Error("failed to restore deploy status after running-state failure",
			zap.String("deploy_id", deployID),
			zap.Error(err),
		)
		return fmt.Errorf("restore deploy status after cleanup: %w", err)
	}
	return nil
}

// probeFailureReason is what the user sees when the probe finds nothing
// listening. It names the cause and the fix, because the provider's own error
// ("UserCodeError: exit status 1") tells them neither.
const probeFailureReason = "the container started but nothing answered on the port the runtime injects. " +
	"Make your server listen on the PORT environment variable."

// probeRuntime asks the backend whether the deploy actually serves. A backend
// that cannot answer the question (no Prober implementation) is not probed, and
// a disabled probe is a config decision rather than a silent default.
func (s *Service) probeRuntime(ctx context.Context, req DeployRequest, result *backend.RunResult) error {
	if !s.cfg.RuntimeProbeEnabled {
		return nil
	}
	prober, ok := s.backend.(backend.Prober)
	if !ok {
		return nil
	}

	s.publishLog(req.DeployID, "runtime-startup", "checking that the application answers on the injected PORT")

	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.RuntimeProbeTimeoutSec)*time.Second)
	defer cancel()

	if err := prober.Probe(probeCtx, backend.ProbeRequest{
		DeployID:    req.DeployID,
		ContainerID: result.ContainerID,
		Port:        req.Port,
		EndpointURL: result.EndpointURL,
	}); err != nil {
		s.log.Warn("runtime probe failed",
			zap.String("deploy_id", req.DeployID),
			zap.Int("port", req.Port),
			zap.Error(err),
		)
		s.publishLogLevel(req.DeployID, "runtime-startup", probeFailureReason, "error")
		return &ProbeError{Err: err}
	}

	s.publishLog(req.DeployID, "runtime-startup", "application answered — deploy is live")
	return nil
}

// teardownAfterProbeFailure removes the runtime that will never serve and
// records the failure, so the saga's compensation finds nothing left to undo.
// Both steps are best-effort: the deploy is already failing, and the watchdog
// and the saga's own status write are the backstops.
func (s *Service) teardownAfterProbeFailure(ctx context.Context, deployID, containerID, imageRef string) {
	if err := s.backend.Stop(ctx, deployID, containerID); err != nil {
		s.log.Error("failed to stop container after probe failure",
			zap.String("deploy_id", deployID),
			zap.String("container_id", containerID),
			zap.Error(err),
		)
	}
	if err := s.RemoveImage(ctx, deployID, imageRef); err != nil {
		s.log.Error("failed to remove image after probe failure",
			zap.String("deploy_id", deployID),
			zap.String("image_ref", imageRef),
			zap.Error(err),
		)
	}
	reason := probeFailureReason
	if err := s.deploys.UpdateDeployStatus(ctx, deployID, "failed", &reason); err != nil {
		s.log.Error("failed to persist probe failure",
			zap.String("deploy_id", deployID), zap.Error(err))
	}
}

// Undeploy stops a running deployment and records the stopped status.
//
// It deliberately leaves the image alone. A stopped deploy is one somebody
// turned off or whose TTL ran out, and Start brings it back by re-running that
// image — seconds instead of a rebuild. Releasing it here would make every
// stop irreversible, which is the whole reason the image sweep skips 'stopped'
// and takes 'failed' and 'deleted' instead. Deleting the deploy is what
// releases the disk, and the retention sweep is what eventually deletes an
// old one.
func (s *Service) Undeploy(ctx context.Context, deployID, containerID string) error {
	s.publishLog(deployID, "runtime-shutdown", "stopping deployment")

	if _, err := s.validateUndeployRequest(ctx, deployID, containerID); err != nil {
		s.publishLogLevel(deployID, "runtime-shutdown", "stop rejected: "+userVisibleError(err), "error")
		return err
	}

	if err := s.backend.Stop(ctx, deployID, containerID); err != nil {
		s.publishLogLevel(deployID, "runtime-shutdown", "stop failed: "+userVisibleError(err), "error")
		s.log.Error("failed to stop container",
			zap.String("deploy_id", deployID),
			zap.String("container_id", containerID),
			zap.Error(err),
		)
		return fmt.Errorf("stop container: %w", err)
	}
	if err := s.deploys.UpdateDeployStatus(ctx, deployID, "stopped", nil); err != nil {
		s.log.Warn("failed to persist stopped status", zap.Error(err))
	}

	s.publishLog(deployID, "runtime-shutdown", "stopped cleanly, image kept for restart")

	s.log.Info("undeploy succeeded",
		zap.String("deploy_id", deployID),
		zap.String("container_id", containerID),
	)

	return nil
}

// RemoveImage idempotently releases one deploy artifact and records that fact
// only after the backend confirms it is absent.
func (s *Service) RemoveImage(ctx context.Context, deployID, imageRef string) error {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return nil
	}
	if err := s.backend.RemoveImage(ctx, imageRef); err != nil {
		s.log.Error("failed to remove deploy image",
			zap.String("deploy_id", deployID),
			zap.String("image_ref", imageRef),
			zap.Error(err),
		)
		return fmt.Errorf("remove deploy image: %w", err)
	}
	if err := s.deploys.MarkDeployImageDeleted(ctx, deployID); err != nil {
		return wrapTransient(fmt.Errorf("record deploy image cleanup: %w", err))
	}
	s.log.Info("deploy image removed", zap.String("deploy_id", deployID), zap.String("image_ref", imageRef))
	return nil
}

func (s *Service) validateUndeployRequest(ctx context.Context, deployID, containerID string) (*deployments.Info, error) {
	info, err := s.deploys.GetDeploy(ctx, deployID)
	if err != nil {
		if errors.Is(err, deployments.ErrNotFound) {
			return nil, &ValidationError{Err: errors.New("deploy not found")}
		}
		return nil, wrapTransient(fmt.Errorf("get deploy state: %w", err))
	}

	if strings.TrimSpace(info.ContainerID) == "" {
		return nil, &ValidationError{Err: errors.New("deploy has no stored container mapping")}
	}
	if info.ContainerID != containerID {
		return nil, &ValidationError{Err: errors.New("container_id does not match the deploy mapping")}
	}
	return info, nil
}

// StopExpired is used by the watchdog for TTL cleanup. It publishes
// explicit TTL lifecycle messages around the same cleanup path as manual
// undeploy, so deploy logs show why the runtime was stopped.
func (s *Service) StopExpired(ctx context.Context, deployID, containerID string) error {
	s.publishLog(deployID, "runtime-shutdown", "watchdog TTL cleanup started")
	if err := s.Undeploy(ctx, deployID, containerID); err != nil {
		s.publishLogLevel(deployID, "runtime-shutdown", "watchdog TTL cleanup failed: "+userVisibleError(err), "error")
		return err
	}
	s.publishLog(deployID, "runtime-shutdown", "watchdog TTL cleanup succeeded")
	return nil
}

// StreamContainerLogs tails container logs and forwards them to the log bus.
// Blocks until the channel closes (container died) or the context is cancelled.
func (s *Service) StreamContainerLogs(ctx context.Context, deployID, containerID string) {
	ch, err := s.backend.StreamLogs(ctx, containerID)
	if err != nil {
		s.log.Warn("failed to open log stream",
			zap.String("deploy_id", deployID),
			zap.String("container_id", containerID),
			zap.Error(err),
		)
		return
	}

	for line := range ch {
		_ = s.publisher.Publish(deployID, logs.LogLine{
			Stage: "runtime",
			Text:  line,
			Level: "info",
		})
	}
}

// HealthCheck delegates to the backend.
func (s *Service) HealthCheck(ctx context.Context, containerID string) (*backend.HealthStatus, error) {
	return s.backend.HealthCheck(ctx, containerID)
}

// BackendName returns the name of the active backend.
func (s *Service) BackendName() string {
	return s.backend.Name()
}

// generateSubdomain produces a deterministic subdomain identifier from a
// deploy UUID. Uses the full UUID (32 hex chars, dashes stripped) for
// collision-free generation — collision space is 2^122, effectively infinite.
//
// The database has a uq_deploys_subdomain UNIQUE constraint as defense in
// depth, but it should never be triggered given the entropy here.
//
// Format: proj-<32-hex>, e.g. proj-269ce67ccbc44d17a720467e50447918
// Total length 37 characters; well within RFC 1035 subdomain label limit (63).
//
// Existing deploys recorded with the older 8-hex format remain routable because
// their stored subdomain is read as-is. New deploys use the full-UUID form.
func generateSubdomain(deployID string) string {
	clean := strings.ReplaceAll(deployID, "-", "")
	return fmt.Sprintf("proj-%s", clean)
}

// validateDeployRequest performs pre-flight checks before invoking the
// backend. Returns *ValidationError for caller-fixable problems,
// ErrAlreadyRunning for idempotent replay, or a transient-wrapped error for
// retriable state-store failures.
//
// Order matters: cheap local checks first (prefix, tag), durable-state lookup
// last (only under StrictImageValidation).
func (s *Service) validateDeployRequest(ctx context.Context, req DeployRequest) error {
	// 1. Registry prefix check (provider-agnostic; values come from config).
	if len(s.cfg.AllowedImagePrefixes) == 0 {
		// Only reachable in non-strict mode (strict + empty list is a
		// fatal startup error). Skip prefix check, log once already at
		// startup via main.go warning.
	} else {
		ok := false
		for _, p := range s.cfg.AllowedImagePrefixes {
			if imageRefMatchesAllowedPrefix(req.ImageRef, p) {
				ok = true
				break
			}
		}
		if !ok {
			return &ValidationError{Err: fmt.Errorf("image_ref %q does not match any allowed image prefix", req.ImageRef)}
		}
	}

	// 2. Tag must equal DeployID (defence against the builder pushing
	// a tag that doesn't correspond to this deploy).
	idx := strings.LastIndex(req.ImageRef, ":")
	if idx < 0 || idx == len(req.ImageRef)-1 {
		return &ValidationError{Err: fmt.Errorf("image_ref %q missing tag", req.ImageRef)}
	}
	tag := req.ImageRef[idx+1:]
	if tag != req.DeployID {
		return &ValidationError{Err: fmt.Errorf("image_ref tag %q does not match deploy_id %q", tag, req.DeployID)}
	}

	// 3. Durable-state cross-check (only in strict mode).
	if !s.cfg.StrictImageValidation {
		return nil
	}

	info, err := s.deploys.GetDeploy(ctx, req.DeployID)
	if err != nil {
		if errors.Is(err, deployments.ErrNotFound) {
			return &ValidationError{Err: errors.New("deploy not found")}
		}
		return wrapTransient(fmt.Errorf("get deploy state: %w", err))
	}

	if info.UserID != req.UserID {
		return &ValidationError{Err: fmt.Errorf("user_id mismatch: request=%s stored=%s", req.UserID, info.UserID)}
	}

	if info.Status == statusRunning {
		return ErrAlreadyRunning
	}

	if !deployableStatuses[info.Status] {
		return &ValidationError{Err: fmt.Errorf("deploy not in deployable state: %s", info.Status)}
	}

	return nil
}

func imageRefMatchesAllowedPrefix(imageRef, prefix string) bool {
	prefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return false
	}
	return strings.HasPrefix(imageRef, prefix+"/")
}

// publishLog is a convenience wrapper.
func (s *Service) publishLog(deployID, stage, text string) {
	s.publishLogLevel(deployID, stage, text, "info")
}

func (s *Service) publishLogLevel(deployID, stage, text, level string) {
	if s.publisher == nil {
		return
	}
	_ = s.publisher.Publish(deployID, logs.LogLine{
		Stage: stage,
		Text:  text,
		Level: level,
	})
}

func userVisibleError(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown error"
	}
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	fields := strings.Fields(msg)
	if len(fields) == 0 {
		return "unknown error"
	}
	msg = strings.Join(fields, " ")
	msg = redactUserVisibleError(msg)
	const maxLen = 240
	if len(msg) > maxLen {
		return msg[:maxLen] + "..."
	}
	return msg
}

var (
	sensitiveAuthPattern   = regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*(bearer\s+)?[^,\s;]+`)
	sensitiveKVPattern     = regexp.MustCompile(`(?i)\b(token|secret|password|private[_-]?key|iam[_-]?token)\s*[:=]\s*[^,\s;]+`)
	sensitiveBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[^,\s;]+`)
)

func redactUserVisibleError(msg string) string {
	msg = sensitiveAuthPattern.ReplaceAllString(msg, `Authorization=[redacted]`)
	msg = sensitiveKVPattern.ReplaceAllString(msg, `${1}=[redacted]`)
	return sensitiveBearerPattern.ReplaceAllString(msg, `Bearer [redacted]`)
}
