// Package runner provides the business logic layer that orchestrates backend
// calls and billing updates for container deployments. HTTP handlers call into
// this service — they never interact with the backend or billing client directly.
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
	"snaphost/internal/runtime/billing"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/logs"
)

// BillingClient is the subset of billing.Client used by Service. It exists
// so tests can inject a fake without spinning up an HTTP server.
type BillingClient interface {
	UpdateDeployStatus(ctx context.Context, deployID string, status string, failureReason *string) error
	SetDeployRunning(ctx context.Context, deployID string, req billing.SetRunningRequest) error
	GetDeploy(ctx context.Context, deployID string) (*billing.DeployInfo, error)
}

// statusRunning indicates a deploy is already live; validateDeployRequest
// returns ErrAlreadyRunning so the HTTP layer can map it to 409.
const statusRunning = "running"

// deployableStatuses lists statuses in which the saga is allowed to invoke
// runner-svc for a deploy. Currently only "building" — saga moves the deploy
// into this status at reservation time and calls runner-svc concurrently with
// the build. After successful backend.Run, saga transitions through
// "provisioning" → "running".
//
// There is NO "built" intermediate status in the canonical state machine —
// see chk_deploys_status constraint in internal/control/db/migrations/0003_deploys.up.sql
// for the full allowed list.
var deployableStatuses = map[string]bool{
	"building": true,
}

// DeployRequest is the input for Service.Deploy, coming from the HTTP handler.
type DeployRequest struct {
	// DeployID is the deploy UUID from user-billing.
	DeployID string
	// UserID is the owning user's UUID.
	UserID string
	// ImageRef is the full image reference produced by builder-svc.
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

// Service orchestrates the backend and billing client for deploy operations.
type Service struct {
	backend   backend.Backend
	billing   BillingClient
	publisher logs.Publisher
	cfg       *config.Config
	log       *zap.Logger
}

// NewService creates a new runner Service.
func NewService(b backend.Backend, billingClient BillingClient, pub logs.Publisher, cfg *config.Config, log *zap.Logger) *Service {
	return &Service{
		backend:   b,
		billing:   billingClient,
		publisher: pub,
		cfg:       cfg,
		log:       log,
	}
}

// Deploy runs a container for the given deploy request. On success it updates
// billing status to "running" and starts a background log-forwarding goroutine.
// On failure it updates billing status to "failed" with the error reason.
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

	// Report provisioning status to billing.
	if err := s.billing.UpdateDeployStatus(ctx, req.DeployID, "provisioning", nil); err != nil {
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
		// Report failure to billing.
		errMsg := err.Error()
		if billingErr := s.billing.UpdateDeployStatus(ctx, req.DeployID, "failed", &errMsg); billingErr != nil {
			s.log.Error("failed to report deploy failure to billing", zap.Error(billingErr))
		}
		return nil, fmt.Errorf("backend run failed: %w", err)
	}

	// Liveness probe (Task 15b). Until this passes, "running" means "started",
	// which is not the same thing: on 2026-07-19 a deploy whose server was
	// bound to a fixed port reached running, committed the user's coins, and
	// answered every request with UserCodeError. Nothing may report running
	// before something answers on the port we injected.
	if err := s.probeRuntime(ctx, req, result); err != nil {
		s.teardownAfterProbeFailure(ctx, req.DeployID, result.ContainerID)
		return nil, err
	}

	// Update billing to running.
	if err := s.billing.SetDeployRunning(ctx, req.DeployID, billing.SetRunningRequest{
		ImageRef:     req.ImageRef,
		EndpointURL:  result.EndpointURL,
		Subdomain:    subdomain,
		ContainerID:  result.ContainerID,
		TTLExpiresAt: result.TTLExpiresAt,
	}); err != nil {
		s.log.Error("failed to report running status to billing", zap.Error(err))
		// Container is running but billing doesn't know — log but don't fail the deploy.
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
// records the failure, so the saga's compensation only has to refund. Both
// steps are best-effort: the deploy is already failing, and the watchdog and
// the saga's own status write are the backstops.
func (s *Service) teardownAfterProbeFailure(ctx context.Context, deployID, containerID string) {
	if err := s.backend.Stop(ctx, deployID, containerID); err != nil {
		s.log.Error("failed to stop container after probe failure",
			zap.String("deploy_id", deployID),
			zap.String("container_id", containerID),
			zap.Error(err),
		)
	}
	reason := probeFailureReason
	if err := s.billing.UpdateDeployStatus(ctx, deployID, "failed", &reason); err != nil {
		s.log.Error("failed to report probe failure to billing",
			zap.String("deploy_id", deployID), zap.Error(err))
	}
}

// Undeploy stops a running deployment and updates billing status.
func (s *Service) Undeploy(ctx context.Context, deployID, containerID string) error {
	s.publishLog(deployID, "runtime-shutdown", "stopping deployment")

	if err := s.validateUndeployRequest(ctx, deployID, containerID); err != nil {
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

	// Report stopped to billing.
	if err := s.billing.UpdateDeployStatus(ctx, deployID, "stopped", nil); err != nil {
		s.log.Warn("failed to report stopped status to billing", zap.Error(err))
	}

	s.publishLog(deployID, "runtime-shutdown", "stopped cleanly")

	s.log.Info("undeploy succeeded",
		zap.String("deploy_id", deployID),
		zap.String("container_id", containerID),
	)

	return nil
}

func (s *Service) validateUndeployRequest(ctx context.Context, deployID, containerID string) error {
	info, err := s.billing.GetDeploy(ctx, deployID)
	if err != nil {
		if errors.Is(err, billing.ErrDeployNotFound) {
			return &ValidationError{Err: errors.New("deploy not found in billing")}
		}
		return wrapTransient(fmt.Errorf("get deploy from billing: %w", err))
	}

	if strings.TrimSpace(info.ContainerID) == "" {
		return &ValidationError{Err: errors.New("deploy has no stored container mapping")}
	}
	if info.ContainerID != containerID {
		return &ValidationError{Err: errors.New("container_id does not match billing deploy mapping")}
	}
	return nil
}

// StopExpired is used by runner-watchdog for TTL cleanup. It publishes
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

// StreamContainerLogs tails container logs and forwards them to the Redis publisher.
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
// Existing deploys recorded with the legacy 8-hex format remain routable —
// their subdomain column is read as-is and matched verbatim by Traefik /
// the Yandex API Gateway. Only deploys created after this change use the
// full-UUID form. No data migration needed.
func generateSubdomain(deployID string) string {
	clean := strings.ReplaceAll(deployID, "-", "")
	return fmt.Sprintf("proj-%s", clean)
}

// validateDeployRequest performs pre-flight checks before invoking the
// backend. Returns *ValidationError for caller-fixable problems (400),
// ErrAlreadyRunning for idempotent-replay (409), or a transient-wrapped
// error for retriable upstream failures (e.g. billing 5xx).
//
// Order matters: cheap local checks first (prefix, tag), billing
// roundtrip last (only under StrictImageValidation).
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

	// 3. Billing cross-check (only in strict mode).
	if !s.cfg.StrictImageValidation {
		return nil
	}

	info, err := s.billing.GetDeploy(ctx, req.DeployID)
	if err != nil {
		if errors.Is(err, billing.ErrDeployNotFound) {
			return &ValidationError{Err: errors.New("deploy not found in billing")}
		}
		// Anything else (network, 5xx, decode error) is transient.
		return wrapTransient(fmt.Errorf("get deploy from billing: %w", err))
	}

	if info.UserID != req.UserID {
		return &ValidationError{Err: fmt.Errorf("user_id mismatch: request=%s billing=%s", req.UserID, info.UserID)}
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
