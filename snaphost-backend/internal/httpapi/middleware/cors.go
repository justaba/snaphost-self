package middleware

import (
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// AllowedOrigins reads CORS_ALLOW_ORIGINS (comma-separated). Empty means no
// cross-origin caller is configured, which for a panel served by this same
// binary is the normal case.
//
// Exported because the WebSocket endpoint needs the same list: it authenticates
// from a cookie, so an unchecked Origin would let any page the operator visits
// open a socket as them. CORS does not cover WebSocket handshakes, so the check
// has to be made there by hand against the same configuration.
func AllowedOrigins() []string {
	raw := os.Getenv("CORS_ALLOW_ORIGINS")
	if raw == "" {
		return nil
	}

	var origins []string
	for _, origin := range strings.Split(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

// CORS returns a gin middleware that configures Cross-Origin Resource Sharing.
//
// Example for local dev in .env:
//
//	CORS_ALLOW_ORIGINS=http://localhost:5173,http://localhost:3000
func CORS() gin.HandlerFunc {
	origins := AllowedOrigins()
	if len(origins) == 0 {
		// No origins configured — return a no-op handler
		return func(c *gin.Context) { c.Next() }
	}

	cfg := cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		// Idempotency-Key is not here: it was read by the saga's HTTP calls to
		// runner-svc, and those are direct in-process calls now. No handler on
		// this engine looks at it.
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	return cors.New(cfg)
}
