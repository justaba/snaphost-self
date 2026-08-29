// Package upload reads (and deletes) source archive blobs stored in
// Redis by user-billing's POST /api/v1/deploys/upload (Task 14b-2). The
// key layout (`upload:<uuid>` hash with `data` and `user_id` fields) is
// mirrored from internal/control/upload — keep the two in sync.
package upload

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "upload:"

const (
	fieldData   = "data"
	fieldUserID = "user_id"
)

// ErrNotFound is returned when an upload id does not exist (expired TTL
// or already consumed).
var ErrNotFound = errors.New("upload not found")

// Store reads uploaded archive blobs.
type Store struct {
	rdb *redis.Client
}

// NewStore constructs a Store over the given Redis client.
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Get returns the archive bytes and the uploader's user id.
func (s *Store) Get(ctx context.Context, uploadID string) (data []byte, userID string, err error) {
	vals, err := s.rdb.HMGet(ctx, keyPrefix+uploadID, fieldData, fieldUserID).Result()
	if err != nil {
		return nil, "", fmt.Errorf("get upload: %w", err)
	}
	raw, ok := vals[0].(string)
	if !ok || raw == "" {
		return nil, "", ErrNotFound
	}
	owner, _ := vals[1].(string)
	return []byte(raw), owner, nil
}

// Delete removes the blob. Called after unpack (success or failure) so
// archives do not sit in Redis until the TTL backstop.
func (s *Store) Delete(ctx context.Context, uploadID string) error {
	if err := s.rdb.Del(ctx, keyPrefix+uploadID).Err(); err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	return nil
}
