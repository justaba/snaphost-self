// Package gitcred reads (and deletes) short-lived git credentials stored
// in Redis by user-billing for git_private deploys (Task 14b-3). Key
// layout (`gitcred:<uuid>` hash with token/username/user_id fields) is
// mirrored from internal/control/gitcred — keep the two in sync.
package gitcred

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "gitcred:"

const (
	fieldToken    = "token"
	fieldUsername = "username"
	fieldUserID   = "user_id"
)

// ErrNotFound is returned when a credential id does not exist (expired
// TTL or already consumed).
var ErrNotFound = errors.New("git credential not found")

// Credential is a short-lived git credential for one clone.
type Credential struct {
	Username string
	Token    string
	UserID   string
}

// Store reads git credentials.
type Store struct {
	rdb *redis.Client
}

// NewStore constructs a Store over the given Redis client.
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Get returns the credential. The caller must Delete it after the clone
// attempt regardless of outcome.
func (s *Store) Get(ctx context.Context, credentialID string) (*Credential, error) {
	vals, err := s.rdb.HMGet(ctx, keyPrefix+credentialID, fieldToken, fieldUsername, fieldUserID).Result()
	if err != nil {
		return nil, fmt.Errorf("get git credential: %w", err)
	}
	token, ok := vals[0].(string)
	if !ok || token == "" {
		return nil, ErrNotFound
	}
	username, _ := vals[1].(string)
	userID, _ := vals[2].(string)
	return &Credential{Username: username, Token: token, UserID: userID}, nil
}

// Delete removes the credential immediately after the clone attempt so
// the secret does not sit in Redis until the TTL backstop.
func (s *Store) Delete(ctx context.Context, credentialID string) error {
	if err := s.rdb.Del(ctx, keyPrefix+credentialID).Err(); err != nil {
		return fmt.Errorf("delete git credential: %w", err)
	}
	return nil
}
