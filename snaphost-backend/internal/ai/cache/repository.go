package cache

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
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
	err := r.pool.QueryRow(ctx, `
		SELECT signature, project_type, dockerfile, expose_port, source
		FROM ai_dockerfile_cache
		WHERE signature = $1
	`, signature).Scan(&c.Signature, &c.ProjectType, &c.Dockerfile, &c.ExposePort, &c.Source)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) Store(ctx context.Context, signature string, dockerfile string, exposePort int, projectType string, source string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ai_dockerfile_cache (signature, project_type, dockerfile, expose_port, source, used_count, last_used_at)
		VALUES ($1, $2, $3, $4, $5, 1, now())
		ON CONFLICT (signature) DO UPDATE SET
			dockerfile = excluded.dockerfile,
			expose_port = excluded.expose_port,
			source = excluded.source,
			last_used_at = now()
	`, signature, projectType, dockerfile, exposePort, source)
	return err
}

func (r *Repository) IncrementUsage(ctx context.Context, signature string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE ai_dockerfile_cache 
		SET used_count = used_count + 1, last_used_at = now() 
		WHERE signature = $1
	`, signature)
	return err
}

func (r *Repository) DeleteExpired(ctx context.Context, ttlDays int) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM ai_dockerfile_cache
		WHERE last_used_at < now() - interval '1 day' * $1
	`, ttlDays)
	return err
}
