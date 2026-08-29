// Command worker starts the builder-svc worker process. It pulls build jobs
// from Redis Streams and executes the full build pipeline: clone → detect →
// validate → build → scan → report.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/builder-svc/config"
	"snaphost/builder-svc/internal/ai"
	"snaphost/builder-svc/internal/build"
	"snaphost/builder-svc/internal/clone"
	"snaphost/builder-svc/internal/events"
	"snaphost/builder-svc/internal/gitcred"
	"snaphost/builder-svc/internal/logs"
	"snaphost/builder-svc/internal/pipeline"
	"snaphost/builder-svc/internal/queue"
	"snaphost/builder-svc/internal/registry"
	"snaphost/builder-svc/internal/scan"
	"snaphost/builder-svc/internal/unpack"
	"snaphost/builder-svc/internal/upload"
	"snaphost/shared/yandexauth"
)

func main() {
	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// Initialize logger.
	var log *zap.Logger
	log, err = zap.NewProduction()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck

	// Connect to Redis.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal("failed to parse redis URL", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatal("failed to ping redis", zap.Error(err))
	}

	// Create queue and ensure consumer group exists.
	q := queue.NewQueue(rdb, log)
	if err := q.EnsureGroup(context.Background()); err != nil {
		log.Fatal("failed to ensure consumer group", zap.Error(err))
	}

	// Create log publisher.
	pub := logs.NewRedisPublisher(rdb, log)

	// Create build event publisher (build-events:{deploy_id} pub/sub).
	eventsPub := events.NewRedisEventPublisher(rdb, log)

	// Create cloner.
	cloner := clone.NewCloner(pub, log)

	// Create BuildKit builder.
	dockerConfigDir := os.Getenv("DOCKER_CONFIG")
	if dockerConfigDir == "" {
		dockerConfigDir = os.ExpandEnv("$HOME/.docker")
	}
	builder, err := build.NewBuilder(cfg.BuildKitHost, dockerConfigDir, cfg, pub, log)
	if err != nil {
		log.Fatal("failed to create buildkit builder", zap.Error(err))
	}
	defer builder.Close()

	// Create scanner. Private-registry credentials are generated just before
	// each scan and written to a host-scoped temporary Docker config.
	var scanCredentials scan.CredentialsProvider
	if cfg.RegistryAuthMode == "yandex_iam" {
		sdk, err := yandexauth.NewSDK(context.Background(), cfg.YandexSAKeyPath)
		if err != nil {
			log.Fatal("failed to create scanner registry auth", zap.Error(err))
		}
		scanCredentials = func(ctx context.Context) (string, string, error) {
			token, err := yandexauth.IAMToken(ctx, sdk)
			return "iam", token, err
		}
	} else if cfg.RegistryUsername != "" || cfg.RegistryPassword != "" {
		scanCredentials = func(context.Context) (string, string, error) {
			return cfg.RegistryUsername, cfg.RegistryPassword, nil
		}
	}
	scanner := scan.NewScanner(pub, log, cfg.RegistryInsecure, registryHost(cfg.RegistryURL), scanCredentials)

	// Create registry cleanup client. Used by the pipeline to remove
	// images that fail Trivy scanning after push.
	// TODO(provider-abstraction): when production cloud backends ship,
	// select the registry client based on cfg/RUNNER_BACKEND. For now
	// Docker v2 is the only implementation; Yandex CR delete is future
	// work behind the `yandex` build tag (see Task 0 isolation pattern).
	registryClient := registry.NewDockerV2Client(log)

	// Create status reporter (HTTP client to user-billing).
	statusReporter := &httpStatusReporter{
		baseURL:       cfg.UserBillingURL,
		webhookSecret: cfg.WebhookSecret,
		client:        &http.Client{Timeout: 10 * time.Second},
		log:           log,
	}

	// Create AI client (HTTP client to ai-orchestrator).
	aiClient := ai.NewHTTPClient(cfg.AIOrchestratorURL, cfg.WebhookSecret, log)

	// Create pipeline runner.
	runner := &pipeline.Runner{
		Cfg:         cfg,
		Queue:       q,
		Publisher:   pub,
		Events:      eventsPub,
		Cloner:      cloner,
		Builder:     builder,
		Scanner:     scanner,
		Status:      statusReporter,
		AIClient:    aiClient,
		Registry:    registryClient,
		Uploads:     upload.NewStore(rdb),
		Credentials: gitcred.NewStore(rdb),
		UnpackLimits: unpack.Limits{
			MaxFiles:      cfg.MaxArchiveFiles,
			MaxFileBytes:  int64(cfg.MaxArchiveFileMB) * 1024 * 1024,
			MaxTotalBytes: int64(cfg.MaxArchiveTotalMB) * 1024 * 1024,
		},
		Log: log,
	}

	// Determine consumer name from hostname.
	consumerName, _ := os.Hostname()
	if consumerName == "" {
		consumerName = "worker-1"
	}

	// Graceful shutdown: on SIGTERM, cancel context so Consume loop exits
	// after the current job finishes.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting builder-svc worker",
		zap.String("consumer", consumerName),
		zap.String("buildkit_host", cfg.BuildKitHost),
		zap.String("registry", cfg.RegistryURL),
	)

	// Block on consuming jobs.
	if err := q.Consume(ctx, consumerName, func(job queue.Job) error {
		log.Info("processing job",
			zap.String("deploy_id", job.DeployID),
			zap.String("user_id", job.UserID),
			zap.String("repo_url", job.RepoURL),
		)
		result, err := runner.Run(ctx, job)
		if err != nil {
			log.Error("pipeline failed",
				zap.String("deploy_id", job.DeployID),
				zap.Bool("transient", pipeline.IsTransient(err)),
				zap.Bool("permanent", pipeline.IsPermanent(err)),
				zap.Error(err),
			)
			// Today: any error (transient or permanent) finalizes the
			// deploy. Saga sees BuildFailed, compensates, refunds.
			// Task 5b will defer FinalizeAsFailed for transient errors
			// pending a retry budget; the ack-on-return-nil decision
			// will also move there.
			runner.FinalizeAsFailed(ctx, job.DeployID, err)
			return nil
		}
		log.Info("pipeline completed",
			zap.String("deploy_id", job.DeployID),
			zap.String("image_ref", result.ImageRef),
		)
		runner.FinalizeAsSucceeded(ctx, job.DeployID, result)
		return nil
	}); err != nil && ctx.Err() == nil {
		log.Fatal("consumer exited unexpectedly", zap.Error(err))
	}

	log.Info("builder-svc worker stopped")
}

func registryHost(registryURL string) string {
	value := strings.TrimSpace(registryURL)
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	host, _, _ := strings.Cut(value, "/")
	return host
}

// httpStatusReporter implements pipeline.StatusReporter via HTTP calls to user-billing.
type httpStatusReporter struct {
	baseURL       string
	webhookSecret string
	client        *http.Client
	log           *zap.Logger
}

// ReportBuilding notifies user-billing that the build has started.
func (r *httpStatusReporter) ReportBuilding(ctx context.Context, deployID string) error {
	return r.postStatus(ctx, "/internal/deploys/"+deployID+"/status", map[string]string{
		"status": "building",
	})
}

// ReportFailed notifies user-billing that the build has failed.
func (r *httpStatusReporter) ReportFailed(ctx context.Context, deployID, reason string) error {
	return r.postStatus(ctx, "/internal/deploys/"+deployID+"/status", map[string]string{
		"status":         "failed",
		"failure_reason": reason,
	})
}

func (r *httpStatusReporter) postStatus(ctx context.Context, path string, body map[string]string) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal status body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", r.webhookSecret)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("send status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("status request returned %d", resp.StatusCode)
	}

	return nil
}
