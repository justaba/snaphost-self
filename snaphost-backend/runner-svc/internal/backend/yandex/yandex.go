//go:build yandex
// +build yandex

package yandex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yandex-cloud/go-genproto/yandex/cloud/access"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/operation"
	apigwpb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/apigateway/v1"
	containerspb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/containers/v1"
	ycsdk "github.com/yandex-cloud/go-sdk"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/logs"
	"snaphost/shared/yandexauth"
)

// maxServerlessExecTimeout caps the per-request execution timeout. Yandex
// Serverless Containers reject anything above ~10 minutes for synchronous use.
const maxServerlessExecTimeout = 600 * time.Second

// YandexBackend deploys user containers to Yandex Cloud Serverless Containers
// and exposes them via a single API Gateway whose spec is mutated per deploy.
type YandexBackend struct {
	sdk *ycsdk.SDK

	folderID    string
	registryURL string
	domainName  string
	gatewayID   string
	runnerSAID  string
	memoryMB    int64
	cpuLimit    float64
	routingMode string

	// Runtime log collection (Task 13a). Operator-only: these settings decide
	// where a user container's stdout/stderr lands in Cloud Logging, and have
	// no effect on the build/lifecycle events the deploy owner streams.
	logGroupID   string
	logsDisabled bool
	logMinLevel  string

	log       *zap.Logger
	publisher logs.Publisher

	// gwMu serializes API Gateway spec mutations within this process. Cross-
	// process races still resolve to last-write-wins, which is acceptable since
	// the saga retries failed deploys.
	gwMu sync.Mutex
}

// NewYandexBackend constructs the backend and one authenticated Yandex SDK
// from the configured service-account authorized-key JSON file.
func NewYandexBackend(cfg *config.Config, publisher logs.Publisher, log *zap.Logger) (*YandexBackend, error) {
	if cfg.YandexSAKeyPath == "" || cfg.YandexFolderID == "" || cfg.YandexRunnerSAID == "" ||
		cfg.YandexAPIGatewayID == "" || cfg.YandexRegistryURL == "" {
		return nil, errors.New("yandex backend: missing required config (YANDEX_SA_KEY_PATH, YANDEX_FOLDER_ID, YANDEX_RUNNER_SA_ID, YANDEX_API_GATEWAY_ID, YANDEX_REGISTRY_URL)")
	}

	sdk, err := yandexauth.NewSDK(context.Background(), cfg.YandexSAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("yandex sdk: %w", err)
	}

	return &YandexBackend{
		sdk:         sdk,
		folderID:    cfg.YandexFolderID,
		registryURL: cfg.YandexRegistryURL,
		domainName:  cfg.DomainSuffix,
		gatewayID:   cfg.YandexAPIGatewayID,
		runnerSAID:  cfg.YandexRunnerSAID,
		memoryMB:    cfg.ContainerMemoryMB,
		cpuLimit:    cfg.ContainerCPULimit,
		routingMode: cfg.YandexRoutingMode,

		logGroupID:   cfg.YandexLogGroupID,
		logsDisabled: cfg.YandexRuntimeLogsDisabled,
		logMinLevel:  cfg.YandexRuntimeLogMinLevel,

		log:       log,
		publisher: publisher,
	}, nil
}

// Name returns the backend identifier.
func (b *YandexBackend) Name() string { return "yandex" }

// Run creates a serverless container, deploys a revision pointing at the user
// image, registers a hostname route on the API Gateway, and returns the public
// URL. Failures after container creation trigger best-effort cleanup.
func (b *YandexBackend) Run(ctx context.Context, req backend.RunRequest) (*backend.RunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	containerName := containerNameFor(req)
	containerID, created, err := b.ensureContainer(ctx, req, containerName)
	if err != nil {
		b.publish(req.DeployID, "Yandex container prepare failed: "+userVisibleYandexError(err), "error")
		return nil, err
	}
	if created {
		b.publish(req.DeployID, "Yandex serverless container created", "info")
	} else {
		b.publish(req.DeployID, "Yandex serverless container ready", "info")
	}

	// Cleanup helper used on any subsequent failure.
	cleanup := func(routeAdded bool, hostname string) {
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bgCancel()
		if routeAdded {
			if err := b.removeRoute(bgCtx, hostname); err != nil {
				b.log.Warn("cleanup: remove route failed", zap.String("host", hostname), zap.Error(err))
			}
		}
		if !created {
			return
		}
		if _, err := b.sdk.Serverless().Containers().Container().Delete(bgCtx, &containerspb.DeleteContainerRequest{ContainerId: containerID}); err != nil {
			b.log.Warn("cleanup: delete container failed", zap.String("container_id", containerID), zap.Error(err))
		}
	}

	b.publish(req.DeployID, "deploying Yandex container revision", "info")

	env := revisionEnv(req)
	ttl := effectiveTTL(req.TTL)

	deployRevision := func(logOpts *containerspb.LogOptions) (*operation.Operation, error) {
		return b.sdk.Serverless().Containers().Container().DeployRevision(ctx, &containerspb.DeployContainerRevisionRequest{
			ContainerId: containerID,
			Resources: &containerspb.Resources{
				Memory:       b.memoryBytes(),
				Cores:        1,
				CoreFraction: b.coreFraction(),
			},
			ExecutionTimeout: durationpb.New(ttl),
			ServiceAccountId: b.runnerSAID,
			ImageSpec: &containerspb.ImageSpec{
				ImageUrl:    req.ImageRef,
				WorkingDir:  "/",
				Environment: env,
			},
			Concurrency:     1,
			ProvisionPolicy: &containerspb.ProvisionPolicy{MinInstances: 0},
			LogOptions:      logOpts,
		})
	}

	logOpts := revisionLogOptions(b.logGroupID, b.logMinLevel, b.logsDisabled)
	rawOp, err := deployRevision(logOpts)
	// Log collection is best-effort and must never be the reason a deploy
	// fails. A misconfigured or inaccessible log group took every new deploy
	// down on 2026-08-04; retrying without the options keeps the platform
	// working while making the loss of logs visible rather than silent.
	if err != nil && isLogOptionsRejection(err) {
		b.log.Warn("runtime log options rejected; retrying without them",
			zap.String("deploy_id", req.DeployID),
			zap.String("log_group_id", b.logGroupID),
			zap.Error(err),
		)
		b.publish(req.DeployID, "runtime log collection is unavailable; deploying without it", "warn")
		rawOp, err = deployRevision(nil)
	}
	deployOp, wrapErr := b.sdk.WrapOperation(rawOp, err)
	err = wrapErr
	if err != nil {
		cleanup(false, "")
		b.publish(req.DeployID, "Yandex revision deploy failed: "+userVisibleYandexError(err), "error")
		return nil, fmt.Errorf("%w: deploy revision: %v", backend.ErrContainerStartFailed, err)
	}
	if err := deployOp.Wait(ctx); err != nil {
		cleanup(false, "")
		b.publish(req.DeployID, "Yandex revision deploy failed: "+userVisibleYandexError(err), "error")
		return nil, fmt.Errorf("%w: wait deploy revision: %v", backend.ErrContainerStartFailed, err)
	}
	b.publish(req.DeployID, "revision deployed", "info")

	hostname := fmt.Sprintf("%s.%s", hostnamePrefix(req, containerName), b.domainName)
	if b.usesCentralRouter() {
		b.publish(req.DeployID, "central router mode: API Gateway route unchanged", "info")
	} else {
		b.publish(req.DeployID, "API Gateway route update started", "info")
		if err := b.addRoute(ctx, hostname, containerID); err != nil {
			cleanup(false, hostname)
			b.publish(req.DeployID, "API Gateway route update failed: "+userVisibleYandexError(err), "error")
			return nil, fmt.Errorf("add gateway route: %w", err)
		}
		b.publish(req.DeployID, "API Gateway route added/updated", "info")
	}
	b.publish(req.DeployID, "public route ready: https://"+hostname, "info")

	now := time.Now().UTC()
	return &backend.RunResult{
		ContainerID:  containerID,
		EndpointURL:  "https://" + hostname,
		StartedAt:    now,
		TTLExpiresAt: now.Add(ttl),
	}, nil
}

func (b *YandexBackend) ensureContainer(ctx context.Context, req backend.RunRequest, name string) (containerID string, created bool, err error) {
	existingID, err := b.findContainerByName(ctx, name)
	if err != nil {
		return "", false, fmt.Errorf("%w: list containers: %v", backend.ErrContainerStartFailed, err)
	}
	if existingID != "" {
		b.publish(req.DeployID, "reusing Yandex serverless container", "info")
		return existingID, false, nil
	}

	b.publish(req.DeployID, "creating Yandex serverless container", "info")
	createOp, err := b.sdk.WrapOperation(b.sdk.Serverless().Containers().Container().Create(ctx, &containerspb.CreateContainerRequest{
		FolderId:    b.folderID,
		Name:        name,
		Description: "SnapHost deploy " + req.DeployID,
		Labels:      yandexResourceLabels(req),
	}))
	if err != nil {
		if isAlreadyExists(err) {
			existingID, findErr := b.findContainerByName(ctx, name)
			if findErr != nil {
				return "", false, fmt.Errorf("%w: find existing container: %v", backend.ErrContainerStartFailed, findErr)
			}
			if existingID != "" {
				return existingID, false, nil
			}
		}
		return "", false, fmt.Errorf("%w: create container: %v", backend.ErrContainerStartFailed, err)
	}
	if err := createOp.Wait(ctx); err != nil {
		return "", false, fmt.Errorf("%w: wait create container: %v", backend.ErrContainerStartFailed, err)
	}
	metadata, err := createOp.Metadata()
	if err != nil {
		return "", false, fmt.Errorf("create container metadata: %w", err)
	}
	createResp, ok := metadata.(*containerspb.CreateContainerMetadata)
	if !ok {
		return "", false, fmt.Errorf("create container metadata: unexpected type %T", metadata)
	}
	return createResp.GetContainerId(), true, nil
}

func yandexResourceLabels(req backend.RunRequest) map[string]string {
	return map[string]string{
		"managed_by": "snaphost",
		"deploy_id":  sanitizeYandexLabelValue(req.DeployID),
		"user_id":    sanitizeYandexLabelValue(req.UserID),
	}
}

func sanitizeYandexLabelValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '@':
			b.WriteRune(r)
		}
		if b.Len() >= 63 {
			break
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func (b *YandexBackend) findContainerByName(ctx context.Context, name string) (string, error) {
	pageToken := ""
	for {
		resp, err := b.sdk.Serverless().Containers().Container().List(ctx, &containerspb.ListContainersRequest{
			FolderId:  b.folderID,
			PageSize:  100,
			PageToken: pageToken,
			Filter:    fmt.Sprintf("name=%q", name),
		})
		if err != nil {
			return "", err
		}
		for _, c := range resp.GetContainers() {
			if c.GetName() == name {
				return c.GetId(), nil
			}
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return "", nil
		}
	}
}

// Stop removes the gateway route then deletes the container. Missing routes
// and missing containers are treated as success so repeated cleanup is safe.
func (b *YandexBackend) Stop(ctx context.Context, deployID, containerID string) error {
	ctx, cancel := withDefaultTimeout(ctx, 2*time.Minute)
	defer cancel()

	var routeErr error
	if !b.usesCentralRouter() {
		b.publishStage(deployID, "runtime-shutdown", "API Gateway route cleanup started", "info")
		hostname, err := b.findHostnameForContainer(ctx, containerID)
		if err != nil {
			b.publishStage(deployID, "runtime-shutdown", "API Gateway route cleanup failed: "+userVisibleYandexError(err), "error")
			b.log.Warn("stop: could not locate hostname for container", zap.String("container_id", containerID), zap.Error(err))
			routeErr = fmt.Errorf("locate gateway route: %w", err)
		} else if hostname != "" {
			if err := b.removeRoute(ctx, hostname); err != nil {
				b.publishStage(deployID, "runtime-shutdown", "API Gateway route cleanup failed: "+userVisibleYandexError(err), "error")
				b.log.Warn("stop: remove route failed",
					zap.String("container_id", containerID),
					zap.String("host", hostname),
					zap.Error(err),
				)
				routeErr = fmt.Errorf("remove gateway route for %s: %w", hostname, err)
			} else {
				b.publishStage(deployID, "runtime-shutdown", "API Gateway route cleanup succeeded", "info")
				b.log.Info("stop: gateway route removed",
					zap.String("container_id", containerID),
					zap.String("host", hostname),
				)
			}
		} else {
			b.publishStage(deployID, "runtime-shutdown", "API Gateway route cleanup skipped: route not found", "warn")
			b.log.Info("stop: no gateway route found for container", zap.String("container_id", containerID))
		}
	} else {
		b.publishStage(deployID, "runtime-shutdown", "route cleanup skipped in router mode", "info")
		b.log.Info("stop: central router mode, skipping per-deploy gateway route cleanup", zap.String("container_id", containerID))
	}

	b.publishStage(deployID, "runtime-shutdown", "container delete started", "info")
	op, err := b.sdk.WrapOperation(b.sdk.Serverless().Containers().Container().Delete(ctx, &containerspb.DeleteContainerRequest{ContainerId: containerID}))
	if err != nil {
		if isNotFound(err) {
			b.publishStage(deployID, "runtime-shutdown", "container already absent", "warn")
			b.log.Info("stop: container already absent", zap.String("container_id", containerID))
			return routeErr
		}
		b.publishStage(deployID, "runtime-shutdown", "container delete failed: "+userVisibleYandexError(err), "error")
		if routeErr != nil {
			return fmt.Errorf("%v; delete container: %w", routeErr, err)
		}
		return fmt.Errorf("delete container: %w", err)
	}
	if err := op.Wait(ctx); err != nil {
		if isNotFound(err) {
			b.publishStage(deployID, "runtime-shutdown", "container already absent", "warn")
			b.log.Info("stop: container already absent", zap.String("container_id", containerID))
			return routeErr
		}
		b.publishStage(deployID, "runtime-shutdown", "container delete failed: "+userVisibleYandexError(err), "error")
		if routeErr != nil {
			return fmt.Errorf("%v; wait delete container: %w", routeErr, err)
		}
		return fmt.Errorf("wait delete container: %w", err)
	}
	b.publishStage(deployID, "runtime-shutdown", "container delete succeeded", "info")
	b.log.Info("stop: container deleted", zap.String("container_id", containerID))
	return routeErr
}

// HealthCheck returns Running=true when the container has an ACTIVE revision.
func (b *YandexBackend) HealthCheck(ctx context.Context, containerID string) (*backend.HealthStatus, error) {
	c, err := b.sdk.Serverless().Containers().Container().Get(ctx, &containerspb.GetContainerRequest{ContainerId: containerID})
	if err != nil {
		if isNotFound(err) {
			return nil, backend.ErrContainerNotFound
		}
		return nil, fmt.Errorf("get container: %w", err)
	}

	if c.GetStatus() == containerspb.Container_ACTIVE {
		return &backend.HealthStatus{Running: true, Message: "active"}, nil
	}
	return &backend.HealthStatus{Running: false, Message: c.GetStatus().String()}, nil
}

// ErrRuntimeLogsAreOperatorOnly is returned instead of a log stream. The
// caller's only use for that stream is publishing it to the deploy owner's
// WebSocket, and a user application's stdout/stderr is deliberately not part
// of what a deploy owner sees.
//
// The reasoning is Task 13's: build output is the user's to act on and stays
// visible to them, while runtime output is noisy and can carry infrastructure
// detail, so users get status plus a recommendation (13b) instead. The logs
// themselves are not discarded — since Task 13a every revision carries
// log_options, so they are in Cloud Logging where an operator can query them.
var ErrRuntimeLogsAreOperatorOnly = errors.New("yandex backend: user container runtime logs are collected for operators in Cloud Logging, not streamed to the deploy owner")

// StreamLogs refuses by design. See ErrRuntimeLogsAreOperatorOnly.
func (b *YandexBackend) StreamLogs(_ context.Context, _ string) (<-chan string, error) {
	return nil, ErrRuntimeLogsAreOperatorOnly
}

// addRoute mutates the gateway spec to point hostname at containerID.
func (b *YandexBackend) addRoute(ctx context.Context, hostname, containerID string) error {
	b.gwMu.Lock()
	defer b.gwMu.Unlock()

	specResp, err := b.sdk.Serverless().APIGateway().ApiGateway().GetOpenapiSpec(ctx, &apigwpb.GetOpenapiSpecRequest{
		ApiGatewayId: b.gatewayID,
		Format:       apigwpb.GetOpenapiSpecRequest_YAML,
	})
	if err != nil {
		return fmt.Errorf("get api gateway spec: %w", err)
	}
	spec, err := LoadSpec([]byte(specResp.GetOpenapiSpec()))
	if err != nil {
		return err
	}
	if err := AddRoute(spec, hostname, containerID, b.runnerSAID); err != nil {
		return err
	}
	updated, err := Marshal(spec)
	if err != nil {
		return err
	}
	op, err := b.sdk.WrapOperation(b.sdk.Serverless().APIGateway().ApiGateway().Update(ctx, &apigwpb.UpdateApiGatewayRequest{
		ApiGatewayId: b.gatewayID,
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"openapi_spec"}},
		Spec:         &apigwpb.UpdateApiGatewayRequest_OpenapiSpec{OpenapiSpec: string(updated)},
	}))
	if err != nil {
		return fmt.Errorf("update api gateway: %w", err)
	}
	return op.Wait(ctx)
}

func (b *YandexBackend) removeRoute(ctx context.Context, hostname string) error {
	b.gwMu.Lock()
	defer b.gwMu.Unlock()

	specResp, err := b.sdk.Serverless().APIGateway().ApiGateway().GetOpenapiSpec(ctx, &apigwpb.GetOpenapiSpecRequest{
		ApiGatewayId: b.gatewayID,
		Format:       apigwpb.GetOpenapiSpecRequest_YAML,
	})
	if err != nil {
		return fmt.Errorf("get api gateway spec: %w", err)
	}
	spec, err := LoadSpec([]byte(specResp.GetOpenapiSpec()))
	if err != nil {
		return err
	}
	RemoveRoute(spec, hostname)
	updated, err := Marshal(spec)
	if err != nil {
		return err
	}
	op, err := b.sdk.WrapOperation(b.sdk.Serverless().APIGateway().ApiGateway().Update(ctx, &apigwpb.UpdateApiGatewayRequest{
		ApiGatewayId: b.gatewayID,
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"openapi_spec"}},
		Spec:         &apigwpb.UpdateApiGatewayRequest_OpenapiSpec{OpenapiSpec: string(updated)},
	}))
	if err != nil {
		return fmt.Errorf("update api gateway: %w", err)
	}
	return op.Wait(ctx)
}

// findHostnameForContainer scans the gateway spec for a route referencing the
// container ID and returns its hostname. Returns "" if not found.
func (b *YandexBackend) findHostnameForContainer(ctx context.Context, containerID string) (string, error) {
	specResp, err := b.sdk.Serverless().APIGateway().ApiGateway().GetOpenapiSpec(ctx, &apigwpb.GetOpenapiSpecRequest{
		ApiGatewayId: b.gatewayID,
		Format:       apigwpb.GetOpenapiSpecRequest_YAML,
	})
	if err != nil {
		return "", err
	}
	spec, err := LoadSpec([]byte(specResp.GetOpenapiSpec()))
	if err != nil {
		return "", err
	}
	return hostnameForContainerInSpec(spec, containerID), nil
}

func hostnameForContainerInSpec(spec *Spec, containerID string) string {
	paths, _ := spec.root["paths"].(map[string]interface{})
	for _, v := range paths {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		host, _ := entry["x-snaphost-host"].(string)
		if host == "" {
			host, _ = entry["x-yc-apigateway-host"].(string)
		}
		method, ok := entry["x-yc-apigateway-any-method"].(map[string]interface{})
		if !ok {
			continue
		}
		integ, ok := method["x-yc-apigateway-integration"].(map[string]interface{})
		if !ok {
			continue
		}
		if cid, _ := integ["container_id"].(string); cid == containerID && host != "" {
			return host
		}
	}
	return ""
}

func (b *YandexBackend) memoryBytes() int64 {
	const mib = 1024 * 1024
	const fallbackMB = 512
	v := b.memoryMB
	if v <= 0 {
		v = containerMemoryFromBackend(b)
	}
	if v <= 0 {
		v = fallbackMB
	}
	v = roundUpToMultiple(v, 128)
	return v * mib
}

func (b *YandexBackend) coreFraction() int64 {
	return coreFractionFromLimit(b.cpuLimit)
}

func (b *YandexBackend) usesCentralRouter() bool {
	return b.routingMode == "router"
}

func revisionEnv(req backend.RunRequest) map[string]string {
	env := make(map[string]string, len(req.Env))
	for k, v := range req.Env {
		if k == "PORT" {
			continue
		}
		env[k] = v
	}
	return env
}

func effectiveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 || ttl > maxServerlessExecTimeout {
		return maxServerlessExecTimeout
	}
	return ttl
}

func containerNameFor(req backend.RunRequest) string {
	base := strings.TrimSpace(req.Subdomain)
	if base == "" {
		base = "proj-" + req.DeployID
	}
	return normalizeYandexName(base, "proj-"+req.DeployID)
}

func hostnamePrefix(req backend.RunRequest, containerName string) string {
	if req.Subdomain != "" {
		return req.Subdomain
	}
	return containerName
}

func normalizeYandexName(name, fallback string) string {
	normalized := normalizeYandexNameOnce(name)
	if validYandexName(normalized) {
		return normalized
	}
	normalized = normalizeYandexNameOnce(fallback)
	if validYandexName(normalized) {
		return normalized
	}
	return "proj-default"
}

func normalizeYandexNameOnce(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastHyphen := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if r == '-' || r == '_' || r == '.' {
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = "proj-" + out
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.TrimRight(out, "-")
}

func validYandexName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	last := name[len(name)-1]
	if !((last >= 'a' && last <= 'z') || (last >= '0' && last <= '9')) {
		return false
	}
	for i := 1; i < len(name)-1; i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func coreFractionFromLimit(limit float64) int64 {
	if limit <= 0 {
		return 50
	}
	fraction := int64(math.Round(limit * 100))
	if fraction < 5 {
		fraction = 5
	}
	if fraction > 100 {
		fraction = 100
	}
	return roundUpToMultiple(fraction, 5)
}

func roundUpToMultiple(v, multiple int64) int64 {
	if multiple <= 0 || v%multiple == 0 {
		return v
	}
	return v + multiple - v%multiple
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// containerMemoryFromBackend exists so memoryBytes can be overridden in tests
// without leaking config into the package interface.
var containerMemoryFromBackend = func(*YandexBackend) int64 { return 512 }

func (b *YandexBackend) publish(deployID, msg, level string) {
	b.publishStage(deployID, "runtime-startup", msg, level)
}

func (b *YandexBackend) publishStage(deployID, stage, msg, level string) {
	if b.publisher == nil {
		return
	}
	_ = b.publisher.Publish(deployID, logs.LogLine{
		Stage:     stage,
		Text:      msg,
		Level:     level,
		Timestamp: time.Now().UTC(),
	})
}

func userVisibleYandexError(err error) string {
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
	msg = redactUserVisibleYandexError(msg)
	const maxLen = 240
	if len(msg) > maxLen {
		return msg[:maxLen] + "..."
	}
	return msg
}

var (
	sensitiveYandexAuthPattern   = regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*(bearer\s+)?[^,\s;]+`)
	sensitiveYandexKVPattern     = regexp.MustCompile(`(?i)\b(token|secret|password|private[_-]?key|iam[_-]?token)\s*[:=]\s*[^,\s;]+`)
	sensitiveYandexBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[^,\s;]+`)
)

func redactUserVisibleYandexError(msg string) string {
	msg = sensitiveYandexAuthPattern.ReplaceAllString(msg, `Authorization=[redacted]`)
	msg = sensitiveYandexKVPattern.ReplaceAllString(msg, `${1}=[redacted]`)
	return sensitiveYandexBearerPattern.ReplaceAllString(msg, `Bearer [redacted]`)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.NotFound {
		return true
	}
	// The SDK wraps gRPC NotFound; a substring match is good enough for the
	// idempotent paths (Stop, HealthCheck) where misclassification is harmless.
	return strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found")
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.AlreadyExists {
		return true
	}
	return strings.Contains(err.Error(), "AlreadyExists") || strings.Contains(err.Error(), "already exists")
}

// SetMemoryAccessor lets the runner wire ContainerMemoryMB into the backend
// without forcing a constructor change. Called once from main.
func SetMemoryAccessor(fn func(*YandexBackend) int64) {
	containerMemoryFromBackend = fn
}

// Compile-time assertions.
var (
	_ backend.Backend = (*YandexBackend)(nil)
	_                 = access.AccessBinding{}
)
