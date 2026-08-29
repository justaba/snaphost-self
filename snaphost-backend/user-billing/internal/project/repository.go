package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no project matches the lookup.
var ErrNotFound = errors.New("project not found")

// Repository persists projects in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository builds a Repository over the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Ensure resolves the project for a source key, creating it on first sight.
// Concurrent deploys of the same source race here, so the insert relies on the
// (user_id, source_key) unique index and falls back to a read.
func (r *Repository) Ensure(ctx context.Context, userID uuid.UUID, sourceKey string) (*Project, error) {
	var p Project
	err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (user_id, slug, source_key)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, source_key) DO UPDATE SET updated_at = now()
		 RETURNING id, user_id, slug, source_key, created_at, updated_at`,
		userID, Slug(sourceKey), sourceKey,
	).Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("ensure project: %w", err)
	}
	return &p, nil
}

// Get returns one project by id.
func (r *Repository) Get(ctx context.Context, projectID uuid.UUID) (*Project, error) {
	var p Project
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, slug, source_key, created_at, updated_at
		 FROM projects WHERE id = $1`,
		projectID,
	).Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &p, nil
}

// ListByUser returns a user's projects, newest first.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Project, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, slug, source_key, created_at, updated_at
		 FROM projects WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return out, nil
}
