package middleware

import (
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORS returns a gin middleware that configures Cross-Origin Resource Sharing.
//
// Allowed origins are read from the CORS_ALLOW_ORIGINS environment variable
// (comma-separated list). If the variable is empty, CORS headers are not added.
//
// Example for local dev in .env:
//
//	CORS_ALLOW_ORIGINS=http://localhost:5173,http://localhost:3000
func CORS() gin.HandlerFunc {
	raw := os.Getenv("CORS_ALLOW_ORIGINS")
	if raw == "" {
		// No origins configured — return a no-op handler
		return func(c *gin.Context) { c.Next() }
	}

	origins := strings.Split(raw, ",")
	for i, o := range origins {
		origins[i] = strings.TrimSpace(o)
	}

	cfg := cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", "Idempotency-Key"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	return cors.New(cfg)
}
