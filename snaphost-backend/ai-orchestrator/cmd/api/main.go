// Command api starts the ai-orchestrator HTTP server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"snaphost/ai-orchestrator/api"
	"snaphost/ai-orchestrator/config"
	"snaphost/ai-orchestrator/db"
	"snaphost/ai-orchestrator/internal/cache"
	"snaphost/ai-orchestrator/internal/llm"
	"snaphost/ai-orchestrator/internal/service"
	"snaphost/ai-orchestrator/internal/usage"
	"snaphost/shared"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync() //nolint:errcheck

	if cfg.RunMigrations {
		if err := db.Migrate(cfg.DatabaseURL); err != nil {
			logger.Fatal("migrations failed", zap.Error(err))
		}
	}

	pool, err := db.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("database connection failed", zap.Error(err))
	}

	cacheRepo := cache.NewRepository(pool)
	usageRepo := usage.NewRepository(pool)

	// Build the LLM client — API key is never logged. The provider is whatever
	// LLM_BASE_URL points at; see config for why that is configuration.
	inner := llm.NewClient(llm.Config{
		APIKey:   cfg.OpenRouterAPIKey,
		BaseURL:  cfg.LLMBaseURL,
		Model:    cfg.OpenRouterModel,
		Referer:  cfg.OpenRouterReferer,
		AppName:  cfg.OpenRouterAppName,
		Timeout:  cfg.LLMTimeout,
		JSONMode: cfg.LLMJSONMode,
	}, logger)

	// Wrap with circuit breaker: open after 10 consecutive failures, reset after 60s.
	llmClient := llm.NewCircuitClient(inner, 10, 60*time.Second)

	svc := service.New(cacheRepo, usageRepo, llmClient, cfg, logger)
	handler := api.NewHandler(svc)

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "ai-orchestrator",
			"llm_model": cfg.OpenRouterModel,
		})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	internal := r.Group("/internal/ai")
	internal.Use(shared.WebhookAuth(cfg.WebhookSecret))
	handler.Register(internal)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("listen failed", zap.Error(err))
		}
	}()

	logger.Info("ai-orchestrator started",
		zap.String("port", cfg.Port),
		zap.String("llm_model", cfg.OpenRouterModel),
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("graceful shutdown failed", zap.Error(err))
	}
	logger.Info("ai-orchestrator stopped")
}
