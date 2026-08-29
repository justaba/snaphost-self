// Package gitcred stores short-lived git credentials for git_private
// deploys (Task 14b-3) as Redis hashes with a TTL. Only the opaque
// credential id travels through the saga job, the deploy_sagas row, and
// the builder queue — the secret itself lives exclusively in this store
// and is deleted by the builder right after the clone (TTL backstop).
// Key layout is mirrored in internal/builder/gitcred — keep in sync.
package gitcred

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const keyPrefix = "gitcred:"

const (
	fieldToken    = "token"
	fieldUsername = "username"
	fieldUserID   = "user_id"
)

// Store writes git credentials for the builder to consume.
type Store struct {
	rdb *redis.Client
}

// NewStore constructs a Store over the given Redis client.
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Put stores a credential under a fresh UUID with the given TTL and
// returns the credential id.
func (s *Store) Put(ctx context.Context, userID, username, token string, ttl time.Duration) (string, error) {
	id := uuid.New().String()
	key := keyPrefix + id
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, fieldUserID, userID, fieldUsername, username, fieldToken, token)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("store git credential: %w", err)
	}
	return id, nil
}
