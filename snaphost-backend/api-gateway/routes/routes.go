package routes

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/api-gateway/config"
	"snaphost/api-gateway/middleware"
	"snaphost/api-gateway/proxy"
)

func registerProxy(api *gin.RouterGroup, path string, target string, logger *zap.Logger, extra ...gin.HandlerFunc) {
	handler := proxy.HTTP(target, logger)
	chain := append(append([]gin.HandlerFunc{}, extra...), handler)
	api.Any(path, chain...)
	api.Any(path+"/*path", chain...)
}

// uploadBodyLimit bounds POST /api/v1/deploys/upload bodies before they
// are proxied, mirroring user-billing's own MaxBytesReader (14b-2). An
// oversized Content-Length is rejected with 413 immediately; a chunked
// or lying body is cut off by MaxBytesReader mid-proxy.
func uploadBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/api/v1/deploys/upload" {
			if c.Request.ContentLength > maxBytes {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
					"error":   "upload_too_large",
					"message": "archive exceeds the maximum upload size",
				})
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// Register configures all routes for the API Gateway.
func Register(r *gin.Engine, cfg *config.Config, logger *zap.Logger) {
	api := r.Group("/api/v1")
	{
		registerProxy(api, "/auth", cfg.Services.UserBilling, logger)
		registerProxy(api, "/profile", cfg.Services.UserBilling, logger)
		registerProxy(api, "/billing", cfg.Services.UserBilling, logger)
		registerProxy(api, "/keys", cfg.Services.UserBilling, logger)
		registerProxy(api, "/deploys", cfg.Services.UserBilling, logger,
			uploadBodyLimit(int64(cfg.MaxUploadSizeMB)*1024*1024))
		registerProxy(api, "/domains", cfg.Services.UserBilling, logger)
		// Operator read surface. Casbin admits it for the admin role only,
		// and user-billing re-checks the forwarded X-User-Role before
		// answering, so a missing policy line does not expose it.
		registerProxy(api, "/admin", cfg.Services.UserBilling, logger)
		registerProxy(api, "/ai", cfg.Services.AIOrchestrator, logger)
	}

	// /ws/logs/:id is registered directly on the engine in main.go
	// (before middleware) so it can authenticate via the ?token= query param.
}

// RegisterRouterIngress exposes exactly the route lookup needed by the Yandex
// router. It is intentionally registered before user JWT/Casbin middleware and
// must never be widened to a generic /internal proxy.
func RegisterRouterIngress(r *gin.Engine, cfg *config.Config, logger *zap.Logger) {
	r.GET(
		"/internal/routes",
		gin.Recovery(),
		middleware.RequestID(),
		middleware.Logger(logger),
		webhookAuth(cfg.WebhookSecret),
		proxy.HTTP(cfg.Services.UserBilling, logger),
	)
}

func webhookAuth(secret string) gin.HandlerFunc {
	expected := sha256.Sum256([]byte(secret))
	return func(c *gin.Context) {
		provided := c.GetHeader("X-Webhook-Secret")
		actual := sha256.Sum256([]byte(provided))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "invalid webhook secret",
			})
			return
		}
		c.Next()
	}
}
