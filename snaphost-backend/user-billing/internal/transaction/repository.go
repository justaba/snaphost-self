// Package transaction provides read-only data access for the transactions ledger.
package transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Transaction represents a single financial event in the vibecoin economy.
type Transaction struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	DeployID       *uuid.UUID `json:"deploy_id,omitempty"`
	Type           string     `json:"type"`
	Amount         int64      `json:"amount"`
	Status         string     `json:"status"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Repository provides read-only data access for transactions.
// Write operations happen inside wallet.Repository within database transactions.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new transaction repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ListByUser returns paginated transactions for the given user, ordered by creation time descending.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Transaction, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, deploy_id, type, amount, status, idempotency_key, created_at, completed_at
		 FROM transactions
		 WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions by user: %w", err)
	}
	defer rows.Close()

	var txns []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.UserID, &t.DeployID, &t.Type, &t.Amount,
			&t.Status, &t.IdempotencyKey, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan transaction row: %w", err)
		}
		txns = append(txns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction rows: %w", err)
	}

	return txns, nil
}

// Get retrieves a single transaction by ID.
func (r *Repository) Get(ctx context.Context, txID uuid.UUID) (*Transaction, error) {
	var t Transaction
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, deploy_id, type, amount, status, idempotency_key, created_at, completed_at
		 FROM transactions
		 WHERE id = $1`,
		txID,
	).Scan(&t.ID, &t.UserID, &t.DeployID, &t.Type, &t.Amount,
		&t.Status, &t.IdempotencyKey, &t.CreatedAt, &t.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("transaction not found: %w", err)
		}
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	return &t, nil
}
