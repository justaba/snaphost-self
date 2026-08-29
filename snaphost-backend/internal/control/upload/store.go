// Package upload stores uploaded source archives as Redis blobs with a
// TTL (Task 14b-2). Redis is the shared channel between user-billing and
// the builder worker, so no new infrastructure is needed; the same
// `upload:<uuid>` hash is read (and deleted) by builder-svc after unpack.
// The TTL is the backstop for uploads that never become a deploy.
package upload

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces upload blobs in Redis. Mirrored in
// internal/builder/upload — keep the two in sync.
const keyPrefix = "upload:"

// Hash fields of an upload key. `data` holds the raw tar.gz bytes;
// `user_id` scopes the blob to its uploader so another user cannot
// deploy it by guessing the id.
const (
	fieldData   = "data"
	fieldUserID = "user_id"
)

// ErrNotFound is returned when an upload id does not exist (never
// stored, expired, or already consumed by a build).
var ErrNotFound = errors.New("upload not found")

// Store provides access to uploaded archive blobs.
type Store struct {
	rdb *redis.Client
}

// NewStore constructs a Store over the given Redis client.
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Put stores an archive blob under a fresh UUID with the given TTL and
// returns the upload id.
func (s *Store) Put(ctx context.Context, userID string, data []byte, ttl time.Duration) (string, error) {
	id := uuid.New().String()
	key := keyPrefix + id
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, fieldUserID, userID, fieldData, data)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("store upload: %w", err)
	}
	return id, nil
}

// Owner returns the user id that uploaded the blob. ErrNotFound if the
// upload does not exist or has expired.
func (s *Store) Owner(ctx context.Context, uploadID string) (string, error) {
	owner, err := s.rdb.HGet(ctx, keyPrefix+uploadID, fieldUserID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get upload owner: %w", err)
	}
	return owner, nil
}
