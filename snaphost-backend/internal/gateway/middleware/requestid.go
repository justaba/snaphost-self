package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ContextKeyRequestID is the key used to store the request ID in the context
const ContextKeyRequestID = "requestID"

// HeaderXRequestID is the standard header for request IDs
const HeaderXRequestID = "X-Request-ID"

// RequestID generates a unique ID for each request and adds it to the context and headers
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := uuid.New().String()

		// Add to context
		c.Set(ContextKeyRequestID, reqID)

		// Set response header
		c.Writer.Header().Set(HeaderXRequestID, reqID)

		// Set request header for downstream services
		c.Request.Header.Set(HeaderXRequestID, reqID)

		c.Next()
	}
}

// Logger logs request details using zap
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)

		reqID, _ := c.Get(ContextKeyRequestID)

		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", latency),
			zap.Any("request_id", reqID),
		)
	}
}
