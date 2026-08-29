package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no active key matches the lookup.
var ErrNotFound = errors.New("api key not found")

// Repository persists API keys in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository builds a Repository over the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Info is the non-secret view of a key returned in listings.
type Info struct {
	ID         uuid.UUID  `json:"id"`
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Create inserts a new key and returns its id and creation time.
func (r *Repository) Create(ctx context.Context, userID uuid.UUID, hash, prefix, name string) (uuid.UUID, time.Time, error) {
	var id uuid.UUID
	var createdAt time.Time
	err := r.pool.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		userID, hash, prefix, name,
	).Scan(&id, &createdAt)
	if err != nil {
		return uuid.Nil, time.Time{}, fmt.Errorf("create api key: %w", err)
	}
	return id, createdAt, nil
}

// VerifyByHash resolves an active (non-revoked) key by its hash to the owning
// user and key id. Returns ErrNotFound if no active key matches.
func (r *Repository) VerifyByHash(ctx context.Context, hash string) (userID, keyID uuid.UUID, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT user_id, id FROM api_keys
		 WHERE key_hash = $1 AND revoked_at IS NULL`,
		hash,
	).Scan(&userID, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("verify api key: %w", err)
	}
	return userID, keyID, nil
}

// List returns all active keys for a user, newest first.
func (r *Repository) List(ctx context.Context, userID uuid.UUID) ([]Info, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, key_prefix, name, created_at, last_used_at
		 FROM api_keys
		 WHERE user_id = $1 AND revoked_at IS NULL
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var out []Info
	for rows.Next() {
		var i Info
		if err := rows.Scan(&i.ID, &i.Prefix, &i.Name, &i.CreatedAt, &i.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}
	return out, nil
}

// Revoke marks a key revoked. It is scoped to the owning user so a caller cannot
// revoke another user's key. Returns ErrNotFound if no active key matched.
func (r *Repository) Revoke(ctx context.Context, userID, keyID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE api_keys SET revoked_at = now()
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		keyID, userID,
	)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastUsed records a successful verification. Best-effort: callers should
// not fail a request if this errors.
func (r *Repository) TouchLastUsed(ctx context.Context, keyID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE api_keys SET last_used_at = now() WHERE id = $1`, keyID)
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}
