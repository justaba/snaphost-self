package usage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageEntry struct {
	DeployID     string
	UserID       string
	Provider     string
	Model        string
	Operation    string
	InputTokens  int
	OutputTokens int
	CostUSDMicro int64
	DurationMs   int
	Success      bool
	ErrorClass   *string
	CacheHit     bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Record(ctx context.Context, entry UsageEntry) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ai_usage_log (
			deploy_id, user_id, provider, model, operation, 
			input_tokens, output_tokens, cost_usd_micro, duration_ms, 
			success, error_class, cache_hit
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, entry.DeployID, entry.UserID, entry.Provider, entry.Model,
		entry.Operation, entry.InputTokens, entry.OutputTokens, entry.CostUSDMicro,
		entry.DurationMs, entry.Success, entry.ErrorClass, entry.CacheHit)
	return err
}
