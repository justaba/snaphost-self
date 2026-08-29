// Package main is the entry point for the runner-svc watchdog process.
// It periodically checks for expired deployments via the user-billing API
// and stops them. This is a separate process from the API server, sharing
// the same Docker image but invoked with a different command.
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/backend/docker"
	"snaphost/runner-svc/internal/billing"
	"snaphost/runner-svc/internal/logs"
	"snaphost/runner-svc/internal/runner"
	"snaphost/runner-svc/internal/watchdog"
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

	// 5. Select backend.
	var b backend.Backend
	switch cfg.RunnerBackend {
	case "docker":
		b, err = docker.NewDockerBackend(cfg, publisher, log)
		if err != nil {
			log.Fatal("failed to initialise docker backend", zap.Error(err))
		}
	case "yandex":
		b, err = newYandexBackend(cfg, publisher, log)
		if err != nil {
			log.Fatal("yandex backend init failed", zap.Error(err))
		}
	default:
		log.Fatal("unknown RUNNER_BACKEND value", zap.String("value", cfg.RunnerBackend))
	}

	log.Info("watchdog backend selected", zap.String("backend", b.Name()))

	// 6. Build runner service.
	svc := runner.NewService(b, billingClient, publisher, cfg, log)

	// 7. Create context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 8. Initialise and run watchdog (blocks until ctx is cancelled).
	wd := watchdog.NewWatchdog(svc, billingClient, cfg, log)
	log.Info("starting watchdog process")
	wd.Run(ctx)

	log.Info("watchdog process stopped")
}
