// Package main is the entry point for the runner-svc API server.
// It initialises all dependencies, selects the execution backend, and serves
// the internal HTTP API for deploying and managing user containers.
package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/runner-svc/api"
	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/backend/docker"
	"snaphost/runner-svc/internal/billing"
	"snaphost/runner-svc/internal/logs"
	"snaphost/runner-svc/internal/runner"
)

func main() {
	// 1. Load configuration.
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// 2. Initialise logger.
	var log *zap.Logger
	if cfg.LogLevel == "debug" {
		log, err = zap.NewDevelopment()
	} else {
		log, err = zap.NewProduction()
	}
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck

	// 3. Initialise Redis client for log publishing.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal("failed to parse REDIS_URL", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	publisher := logs.NewRedisPublisher(rdb, log)

	// 4. Initialise billing client.
	billingClient := billing.NewClient(cfg.UserBillingURL, cfg.WebhookSecret, log)

	if !cfg.StrictImageValidation && len(cfg.AllowedRegistryPrefixes) == 0 {
		log.Warn("STRICT_IMAGE_VALIDATION=false and REGISTRY_ALLOWED_PREFIXES empty: any image_ref will be accepted (dev-only safe configuration)")
	}

	// 5. Select backend. Docker is the only one: this platform runs on the
	// operator's own host, so there is no cloud runtime to dispatch to. The
	// backend.Backend interface stays because it is what keeps the runtime
	// swappable at all (ADR 0004), not because a second backend exists.
	var b backend.Backend
	switch cfg.RunnerBackend {
	case "docker":
		b, err = docker.NewDockerBackend(cfg, publisher, log)
		if err != nil {
			log.Fatal("failed to initialise docker backend", zap.Error(err))
		}
	default:
		log.Fatal("unknown RUNNER_BACKEND value", zap.String("value", cfg.RunnerBackend))
	}

	log.Info("backend selected", zap.String("backend", b.Name()))

	// 6. Build runner service.
	svc := runner.NewService(b, billingClient, publisher, cfg, log)

	// 7. Create Gin engine.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 8. Health check and metrics (no auth required).
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "runner-svc",
			"backend": b.Name(),
		})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// 9. Internal routes with webhook-secret middleware.
	handler := api.NewHandler(svc, log)
	internal := r.Group("/internal")
	internal.Use(api.WebhookSecretMiddleware(cfg.WebhookSecret))
	{
		internal.POST("/deploys", handler.Deploy)
		internal.DELETE("/deploys/:id", handler.Undeploy)
		internal.GET("/deploys/:id/status", handler.DeployStatus)
	}

	// 10. Start HTTP server with graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		log.Info("starting runner-svc API server", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	// Block until shutdown signal.
	<-ctx.Done()
	log.Info("shutting down gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", zap.Error(err))
	}

	log.Info("runner-svc API server stopped")
}
