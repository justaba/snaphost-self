package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a fixed-window per-user attach limiter over Redis. Attach is
// a rare, deliberate action, so a coarse window is enough — this exists to cap
// the DNS lookups and certificate issuance attempts one account can trigger,
// not to shape traffic.
type RedisLimiter struct {
	rdb    *redis.Client
	max    int
	window time.Duration
}

// NewRedisLimiter allows max attaches per user per window.
func NewRedisLimiter(rdb *redis.Client, max int, window time.Duration) *RedisLimiter {
	return &RedisLimiter{rdb: rdb, max: max, window: window}
}

// Allow reports whether this user may attach another domain right now.
func (l *RedisLimiter) Allow(ctx context.Context, userID string) (bool, error) {
	if l == nil || l.rdb == nil || l.max <= 0 {
		return true, nil
	}
	key := "domain-attach:" + userID
	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("domain attach limiter: %w", err)
	}
	if count == 1 {
		// First hit in this window owns the expiry.
		if err := l.rdb.Expire(ctx, key, l.window).Err(); err != nil {
			return false, fmt.Errorf("domain attach limiter expiry: %w", err)
		}
	}
	return count <= int64(l.max), nil
}
