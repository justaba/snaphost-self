package apikey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

// ErrNotFound is returned when no active key matches the lookup.
var ErrNotFound = errors.New("api key not found")

// Repository persists API keys.
type Repository struct {
	db *sql.DB
}

// NewRepository builds a Repository over the given handle.
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
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
	// The id is generated here rather than by the database: SQLite has no
	// gen_random_uuid().
	id := uuid.New()
	createdAt := time.Now().UTC()

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), userID.String(), hash, prefix, name, controldb.FormatTime(createdAt),
	)
	if err != nil {
		return uuid.Nil, time.Time{}, fmt.Errorf("create api key: %w", err)
	}
	// Truncated to the stored precision so the value returned to the caller is
	// the value a later read will produce.
	return id, createdAt.Truncate(time.Millisecond), nil
}

// VerifyByHash resolves an active (non-revoked) key by its hash to the owning
// user and key id. Returns ErrNotFound if no active key matches.
func (r *Repository) VerifyByHash(ctx context.Context, hash string) (userID, keyID uuid.UUID, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT user_id, id FROM api_keys
		 WHERE key_hash = ? AND revoked_at IS NULL`,
		hash,
	).Scan(&userID, &keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("verify api key: %w", err)
	}
	return userID, keyID, nil
}

// List returns all active keys for a user, newest first.
func (r *Repository) List(ctx context.Context, userID uuid.UUID) ([]Info, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, key_prefix, name, created_at, last_used_at
		 FROM api_keys
		 WHERE user_id = ? AND revoked_at IS NULL
		 ORDER BY created_at DESC`,
		userID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var out []Info
	for rows.Next() {
		var i Info
		if err := rows.Scan(&i.ID, &i.Prefix, &i.Name,
			controldb.Into(&i.CreatedAt), controldb.IntoNull(&i.LastUsedAt)); err != nil {
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
	result, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ?
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		controldb.Now(), keyID.String(), userID.String(),
	)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastUsed records a successful verification. Best-effort: callers should
// not fail a request if this errors.
func (r *Repository) TouchLastUsed(ctx context.Context, keyID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, controldb.Now(), keyID.String())
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}
