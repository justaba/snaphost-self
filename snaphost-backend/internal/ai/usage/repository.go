package usage

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
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
	db *sql.DB
}

func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

func (r *Repository) Record(ctx context.Context, entry UsageEntry) error {
	// The id is generated here: SQLite has no gen_random_uuid(), and created_at
	// comes from the column default.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO ai_usage_log (
			id, deploy_id, user_id, provider, model, operation,
			input_tokens, output_tokens, cost_usd_micro, duration_ms,
			success, error_class, cache_hit
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), entry.DeployID, entry.UserID, entry.Provider, entry.Model,
		entry.Operation, entry.InputTokens, entry.OutputTokens, entry.CostUSDMicro,
		entry.DurationMs, entry.Success, entry.ErrorClass, entry.CacheHit)
	return err
}
