package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/api-gateway/config"
	"snaphost/api-gateway/middleware"
	"snaphost/api-gateway/routes"
	"snaphost/api-gateway/webhooks"
	"snaphost/api-gateway/wslogs"
)

var logger *zap.Logger

// simpleBillingClient implements webhooks.BillingClient. The webhookSecret
// is the shared secret user-billing's WebhookSecretMiddleware checks; it
// must match WEBHOOK_SECRET in user-billing's environment.
type simpleBillingClient struct {
	client        *http.Client
	billingURL    string
	webhookSecret string
}

func (s *simpleBillingClient) CreateUser(ctx context.Context, req webhooks.CreateUserRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.billingURL+"/internal/users", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Webhook-Secret", s.webhookSecret)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("billing service returned status %d", resp.StatusCode)
	}

	return nil
}

func main() {
	var err error
	logger, err = zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger")
	}
	defer logger.Sync() //nolint:errcheck // flushing on exit; nothing left to report a failure to

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatal("Failed to parse REDIS_URL", zap.Error(err))
	}
	rdb := redis.NewClient(redisOpts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}

	enforcer, err := casbin.NewEnforcer("rbac_model.conf", "rbac_policy.csv")
	if err != nil {
		logger.Fatal("Failed to initialize casbin enforcer", zap.Error(err))
	}

	// Initialize JWKS Cache and prefetch
	jwksCache := middleware.NewJWKSCache(cfg.SupabaseURL)
	ctxFetch, cancelFetch := context.WithTimeout(context.Background(), 10*time.Second)
	if err := jwksCache.Prefetch(ctxFetch); err != nil {
		cancelFetch()
		logger.Fatal("Failed to prefetch JWKS", zap.Error(err))
	}
	cancelFetch()

	billingHTTPClient := &http.Client{Timeout: 5 * time.Second}
	billingClient := &simpleBillingClient{
		client:        billingHTTPClient,
		billingURL:    cfg.Services.UserBilling,
		webhookSecret: cfg.WebhookSecret,
	}

	webhookHandler := webhooks.NewWebhookHandler(cfg, billingClient, logger)

	// API-key verifier resolves "sk_" bearer credentials against user-billing.
	apiKeyVerifier := middleware.NewAPIKeyVerifier(cfg.Services.UserBilling, cfg.WebhookSecret)

	r := gin.New()
	routes.RegisterRouterIngress(r, cfg, logger)

	// Webhooks (registered BEFORE all middleware)
	webhooks.Register(r, webhookHandler)

	// Registered BEFORE all middleware
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "api-gateway"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// WebSocket log stream — registered BEFORE middleware so JWT/Casbin
	// don't fight the query-param auth scheme used by browser WebSockets.
	r.GET("/ws/logs/:id", wslogs.Handler(rdb, jwksCache, logger))

	// Register middleware in exact order
	r.Use(gin.Recovery())
	r.Use(middleware.CORS()) // must be first to handle OPTIONS preflight
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.Use(middleware.JWT(cfg, jwksCache, apiKeyVerifier))
	r.Use(middleware.RateLimit(rdb, cfg))
	r.Use(middleware.Casbin(enforcer))
	r.Use(middleware.Enrich())

	routes.Register(r, cfg, logger)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server forced to shutdown", zap.Error(err))
	}

	logger.Info("Server exiting")
}
