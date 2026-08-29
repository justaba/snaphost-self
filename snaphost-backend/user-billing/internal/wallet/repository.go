// Package wallet provides the repository, service, and HTTP handler layers
// for managing user wallets and vibecoin operations.
package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors for wallet operations.
var (
	// ErrWalletNotFound is returned when no wallet exists for the given user ID.
	ErrWalletNotFound = errors.New("wallet not found")
	// ErrInsufficientBalance is returned when the user's available balance is too low.
	ErrInsufficientBalance = errors.New("insufficient balance")
	// ErrTransactionNotFound is returned when the referenced transaction does not exist or is not in the expected state.
	ErrTransactionNotFound = errors.New("transaction not found")
)

// Wallet represents a user's vibecoin wallet.
type Wallet struct {
	UserID    uuid.UUID `json:"user_id"`
	Balance   int64     `json:"balance"`
	Reserved  int64     `json:"reserved"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReserveRequest holds the parameters for reserving vibecoins for a deploy.
type ReserveRequest struct {
	UserID         uuid.UUID `json:"user_id"`
	DeployID       uuid.UUID `json:"deploy_id"`
	Amount         int64     `json:"amount"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// Repository provides data access methods for wallet and related transaction operations.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new wallet repository backed by the given connection pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts a new wallet for the given user with the specified initial balance.
// The operation is idempotent — if a wallet already exists for this user, it is a no-op.
func (r *Repository) Create(ctx context.Context, userID uuid.UUID, initialBalance int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO wallets (user_id, balance) VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`,
		userID, initialBalance,
	)
	if err != nil {
		return fmt.Errorf("create wallet: %w", err)
	}
	return nil
}

// UpsertUser records the account behind a user_id. It is called from the same
// webhook that seeds the wallet, and is idempotent because that webhook can be
// redelivered. A later call with a non-empty email fills one in for a row
// backfilled without one; an empty email never overwrites a stored value,
// since losing an address to a retry with less information would be silent.
func (r *Repository) UpsertUser(ctx context.Context, userID uuid.UUID, email string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users (id, email) VALUES ($1, nullif($2, ''))
		 ON CONFLICT (id) DO UPDATE
		 SET email = coalesce(nullif($2, ''), users.email)`,
		userID, email,
	)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	return nil
}

// Get retrieves the wallet for the given user ID. Returns ErrWalletNotFound if
// no wallet exists.
func (r *Repository) Get(ctx context.Context, userID uuid.UUID) (*Wallet, error) {
	w := &Wallet{}
	err := r.pool.QueryRow(ctx,
		`SELECT user_id, balance, reserved, created_at, updated_at FROM wallets WHERE user_id = $1`,
		userID,
	).Scan(&w.UserID, &w.Balance, &w.Reserved, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWalletNotFound
		}
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return w, nil
}

// Reserve atomically locks vibecoins for a deploy using the saga reservation pattern.
// It is idempotent on the idempotency key — duplicate calls return the existing
// transaction ID without modifying the wallet.
func (r *Repository) Reserve(ctx context.Context, req ReserveRequest) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin reserve tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Idempotency check: if a transaction with this key already exists, return it.
	var existingID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM transactions WHERE idempotency_key = $1`,
		req.IdempotencyKey,
	).Scan(&existingID)
	if err == nil {
		return existingID.String(), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("idempotency check: %w", err)
	}

	// Lock the wallet row and check balance.
	var balance, reserved int64
	err = tx.QueryRow(ctx,
		`SELECT balance, reserved FROM wallets WHERE user_id = $1 FOR UPDATE`,
		req.UserID,
	).Scan(&balance, &reserved)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrWalletNotFound
		}
		return "", fmt.Errorf("lock wallet: %w", err)
	}

	if balance < req.Amount {
		return "", ErrInsufficientBalance
	}

	// Debit balance, credit reserved.
	_, err = tx.Exec(ctx,
		`UPDATE wallets SET balance = balance - $1, reserved = reserved + $1, updated_at = now() WHERE user_id = $2`,
		req.Amount, req.UserID,
	)
	if err != nil {
		return "", fmt.Errorf("update wallet for reserve: %w", err)
	}

	// Record the reservation transaction.
	var txID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO transactions (user_id, deploy_id, type, amount, status, idempotency_key)
		 VALUES ($1, $2, 'reserve', $3, 'pending', $4)
		 RETURNING id`,
		req.UserID, req.DeployID, req.Amount, req.IdempotencyKey,
	).Scan(&txID)
	if err != nil {
		return "", fmt.Errorf("insert reserve transaction: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit reserve tx: %w", err)
	}

	return txID.String(), nil
}

// Commit finalises a pending reservation by releasing the reserved amount.
// The wallet balance is not changed — the reserved funds are simply consumed.
func (r *Repository) Commit(ctx context.Context, txID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin commit tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var userID uuid.UUID
	var amount int64
	err = tx.QueryRow(ctx,
		`SELECT user_id, amount FROM transactions WHERE id = $1 AND type = 'reserve' AND status = 'pending' FOR UPDATE`,
		txID,
	).Scan(&userID, &amount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransactionNotFound
		}
		return fmt.Errorf("lock transaction for commit: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE wallets SET reserved = reserved - $1, updated_at = now() WHERE user_id = $2`,
		amount, userID,
	)
	if err != nil {
		return fmt.Errorf("update wallet for commit: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE transactions SET status = 'completed', completed_at = now() WHERE id = $1`,
		txID,
	)
	if err != nil {
		return fmt.Errorf("update transaction status for commit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit commit tx: %w", err)
	}

	return nil
}

// Refund reverses a pending reservation by returning reserved funds to the
// user's available balance.
func (r *Repository) Refund(ctx context.Context, txID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refund tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var userID uuid.UUID
	var amount int64
	err = tx.QueryRow(ctx,
		`SELECT user_id, amount FROM transactions WHERE id = $1 AND type = 'reserve' AND status = 'pending' FOR UPDATE`,
		txID,
	).Scan(&userID, &amount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransactionNotFound
		}
		return fmt.Errorf("lock transaction for refund: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE wallets SET balance = balance + $1, reserved = reserved - $1, updated_at = now() WHERE user_id = $2`,
		amount, userID,
	)
	if err != nil {
		return fmt.Errorf("update wallet for refund: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE transactions SET status = 'cancelled', completed_at = now() WHERE id = $1`,
		txID,
	)
	if err != nil {
		return fmt.Errorf("update transaction status for refund: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refund tx: %w", err)
	}

	return nil
}

// Topup adds vibecoins to a user's balance. The operation is idempotent on the
// idempotency key — duplicate calls return the existing transaction ID.
func (r *Repository) Topup(ctx context.Context, userID uuid.UUID, amount int64, idempotencyKey string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin topup tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Idempotency check.
	var existingID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM transactions WHERE idempotency_key = $1`,
		idempotencyKey,
	).Scan(&existingID)
	if err == nil {
		return existingID.String(), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("idempotency check: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE wallets SET balance = balance + $1, updated_at = now() WHERE user_id = $2`,
		amount, userID,
	)
	if err != nil {
		return "", fmt.Errorf("update wallet for topup: %w", err)
	}

	var txID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO transactions (user_id, type, amount, status, idempotency_key)
		 VALUES ($1, 'topup', $2, 'completed', $3)
		 RETURNING id`,
		userID, amount, idempotencyKey,
	).Scan(&txID)
	if err != nil {
		return "", fmt.Errorf("insert topup transaction: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit topup tx: %w", err)
	}

	return txID.String(), nil
}
