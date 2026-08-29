package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"snaphost/internal/control/db"
)

// ErrNotFound is returned when no project matches the lookup.
var ErrNotFound = errors.New("project not found")

// Repository persists projects.
type Repository struct {
	db *sql.DB
}

// NewRepository builds a Repository over the given handle.
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

const projectColumns = `id, user_id, slug, source_key, created_at, updated_at`

// scanProject consumes projectColumns in order. Ids scan straight into
// uuid.UUID; timestamps are TEXT and go through db.Into.
func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey,
		db.Into(&p.CreatedAt), db.Into(&p.UpdatedAt))
	return p, err
}

// Ensure resolves the project for a source key, creating it on first sight.
// Concurrent deploys of the same source race here, so the insert relies on the
// (user_id, source_key) unique index and falls back to updating the row that
// won.
func (r *Repository) Ensure(ctx context.Context, userID uuid.UUID, sourceKey string) (*Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx,
		`INSERT INTO projects (id, user_id, slug, source_key)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id, source_key) DO UPDATE SET updated_at = ?
		 RETURNING `+projectColumns,
		uuid.NewString(), userID.String(), Slug(sourceKey), sourceKey, db.Now(),
	))
	if err != nil {
		return nil, fmt.Errorf("ensure project: %w", err)
	}
	return &p, nil
}

// Get returns one project by id.
func (r *Repository) Get(ctx context.Context, projectID uuid.UUID) (*Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = ?`,
		projectID.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &p, nil
}

// ListByUser returns a user's projects, newest first.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Project, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE user_id = ? ORDER BY created_at DESC`,
		userID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return out, nil
}
