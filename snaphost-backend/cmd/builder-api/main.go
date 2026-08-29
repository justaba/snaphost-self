// Command api starts the builder-svc HTTP server. It accepts build requests,
// validates them, and enqueues jobs to Redis Streams for worker processes.
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

	"snaphost/internal/builder/api"
	"snaphost/internal/builder/config"
	"snaphost/internal/builder/queue"
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

	// Create handler.
	handler := api.NewHandler(q, cfg, log)

	// Setup Gin.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", handler.Health)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.POST("/api/v1/build", handler.Build)

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		log.Info("starting builder-svc API", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down API gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", zap.Error(err))
	}

	log.Info("builder-svc API stopped")
}
