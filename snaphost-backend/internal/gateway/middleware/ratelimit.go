package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"snaphost/internal/gateway/config"
)

// RateLimit implements a sliding window rate limiter backed by Redis
func RateLimit(rdb *redis.Client, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// IP Rate Limiting
		ip := c.ClientIP()
		ipKey := fmt.Sprintf("rl:ip:%s", ip)
		allowed, remaining, err := checkRateLimit(c.Request.Context(), rdb, ipKey, cfg.RateLimitIP, time.Minute)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "rate_limit_error"})
			return
		}

		if !allowed {
			setRateLimitHeaders(c, cfg.RateLimitIP, remaining, 60)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests", "retry_after": 60})
			return
		}

		// Set initial headers for IP limit
		setRateLimitHeaders(c, cfg.RateLimitIP, remaining, 0)

		// User Rate Limiting.
		// A user id of the wrong type means something upstream stored the
		// wrong thing; skip per-user limiting rather than panicking on the
		// request path. The IP limit above has already been applied either way.
		userID, exists := c.Get(ContextKeyUserID)
		uidStr, isString := userID.(string)
		if exists && isString {
			userKey := fmt.Sprintf("rl:user:%s", uidStr)
			uAllowed, uRemaining, err := checkRateLimit(c.Request.Context(), rdb, userKey, cfg.RateLimitUser, time.Minute)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "rate_limit_error"})
				return
			}
			if !uAllowed {
				setRateLimitHeaders(c, cfg.RateLimitUser, uRemaining, 60)
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests", "retry_after": 60})
				return
			}
			setRateLimitHeaders(c, cfg.RateLimitUser, uRemaining, 0)

			// Deploy Endpoints Rate Limiting
			if c.Request.Method == http.MethodPost && c.FullPath() == "/api/v1/deploys" {
				deployKey := fmt.Sprintf("rl:deploy:%s", uidStr)
				dAllowed, dRemaining, err := checkRateLimit(c.Request.Context(), rdb, deployKey, cfg.RateLimitDeploy, time.Minute)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "rate_limit_error"})
					return
				}
				if !dAllowed {
					setRateLimitHeaders(c, cfg.RateLimitDeploy, dRemaining, 60)
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests", "retry_after": 60})
					return
				}
				setRateLimitHeaders(c, cfg.RateLimitDeploy, dRemaining, 0)
			}
		}

		c.Next()
	}
}

// checkRateLimit implements the sliding window rate limit check in Redis
func checkRateLimit(ctx context.Context, rdb *redis.Client, key string, limit int, window time.Duration) (bool, int, error) {
	now := time.Now()
	windowStart := now.Add(-window).UnixNano()
	nowNano := now.UnixNano()

	pipe := rdb.TxPipeline()
	// Remove old entries outside the window
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", windowStart))
	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowNano), Member: float64(nowNano)})
	// Count requests in window
	countCmd := pipe.ZCard(ctx, key)
	// Set TTL to window length
	pipe.Expire(ctx, key, window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, 0, err
	}

	count := int(countCmd.Val())
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}

	return count <= limit, remaining, nil
}

func setRateLimitHeaders(c *gin.Context, limit, remaining, retryAfter int) {
	c.Writer.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if retryAfter > 0 {
		c.Writer.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
}
