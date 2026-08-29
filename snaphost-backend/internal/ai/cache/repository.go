package cache

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"database/sql"

	controldb "snaphost/internal/control/db"

	"snaphost/internal/ai/llm"
)

var ErrCacheMiss = errors.New("cache miss")

type CachedDockerfile struct {
	Signature   string
	ProjectType string
	Dockerfile  string
	ExposePort  int
	Source      string
}

type Repository struct {
	db *sql.DB
}

func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

func (r *Repository) ComputeSignature(req llm.GenerateRequest) string {
	h := sha256.New()

	// Schema version. Mixed in first so that bumping cacheSchemaVersion
	// changes every signature, making old rows unreachable. See version.go.
	h.Write([]byte(cacheSchemaVersion))
	h.Write([]byte{0}) // delimiter — prevents accidental collision with file content

	// Format tree deterministically
	tree := append([]string{}, req.FileTree...)
	sort.Strings(tree)
	h.Write([]byte(strings.Join(tree, "\n")))

	// Key files deterministically
	var keys []string
	for k := range req.KeyFiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0}) // delimiter between key name and content
		h.Write([]byte(req.KeyFiles[k]))
		h.Write([]byte{0}) // delimiter between consecutive (k, v) pairs
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

func (r *Repository) Get(ctx context.Context, signature string) (*CachedDockerfile, error) {
	var c CachedDockerfile
	err := r.db.QueryRowContext(ctx, `
		SELECT signature, project_type, dockerfile, expose_port, source
		FROM ai_dockerfile_cache
		WHERE signature = ?
	`, signature).Scan(&c.Signature, &c.ProjectType, &c.Dockerfile, &c.ExposePort, &c.Source)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) Store(ctx context.Context, signature string, dockerfile string, exposePort int, projectType string, source string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO ai_dockerfile_cache (signature, project_type, dockerfile, expose_port, source, used_count, last_used_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT (signature) DO UPDATE SET
			dockerfile = excluded.dockerfile,
			expose_port = excluded.expose_port,
			source = excluded.source,
			last_used_at = ?
	`, signature, projectType, dockerfile, exposePort, source, controldb.Now(), controldb.Now())
	return err
}

func (r *Repository) IncrementUsage(ctx context.Context, signature string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ai_dockerfile_cache
		SET used_count = used_count + 1, last_used_at = ?
		WHERE signature = ?
	`, controldb.Now(), signature)
	return err
}

func (r *Repository) DeleteExpired(ctx context.Context, ttlDays int) error {
	// The cutoff is computed in Go rather than in SQL. SQLite's date functions
	// produce 'YYYY-MM-DD HH:MM:SS', which does not compare correctly against
	// the RFC 3339 text these columns hold — the space where the T belongs
	// sorts before every digit, so every row would look expired.
	cutoff := controldb.FormatTime(time.Now().AddDate(0, 0, -ttlDays))
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM ai_dockerfile_cache
		WHERE last_used_at < ?
	`, cutoff)
	return err
}
