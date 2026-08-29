package wallet

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Service provides business-logic operations on wallets. It wraps Repository
// with logging and validation. Notifications, audit logging, and metrics will
// be added here in future iterations.
type Service struct {
	repo *Repository
	log  *zap.Logger
}

// NewService creates a new wallet service.
func NewService(repo *Repository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Create provisions a new wallet for the given user with the specified initial
// balance and records the account itself. The email is what makes an account
// identifiable to an operator later; it arrives only on this webhook, so a
// failure to store it cannot be repaired from this database afterwards.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, email string, initialBalance int64) error {
	s.log.Info("creating wallet",
		zap.String("user_id", userID.String()),
		zap.Int64("initial_balance", initialBalance),
	)
	if err := s.repo.UpsertUser(ctx, userID, email); err != nil {
		return err
	}
	return s.repo.Create(ctx, userID, initialBalance)
}

// Get retrieves the wallet for the given user.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (*Wallet, error) {
	return s.repo.Get(ctx, userID)
}

// Reserve locks vibecoins for a deploy.
func (s *Service) Reserve(ctx context.Context, req ReserveRequest) (string, error) {
	s.log.Info("reserving vibecoins",
		zap.String("user_id", req.UserID.String()),
		zap.String("deploy_id", req.DeployID.String()),
		zap.Int64("amount", req.Amount),
		zap.String("idempotency_key", req.IdempotencyKey),
	)
	return s.repo.Reserve(ctx, req)
}

// Commit finalises a pending reservation.
func (s *Service) Commit(ctx context.Context, txID uuid.UUID) error {
	s.log.Info("committing reservation", zap.String("tx_id", txID.String()))
	return s.repo.Commit(ctx, txID)
}

// Refund reverses a pending reservation, returning funds to the user's balance.
func (s *Service) Refund(ctx context.Context, txID uuid.UUID) error {
	s.log.Info("refunding reservation", zap.String("tx_id", txID.String()))
	return s.repo.Refund(ctx, txID)
}

// Topup adds vibecoins to a user's balance.
func (s *Service) Topup(ctx context.Context, userID uuid.UUID, amount int64, idempotencyKey string) (string, error) {
	s.log.Info("topping up wallet",
		zap.String("user_id", userID.String()),
		zap.Int64("amount", amount),
		zap.String("idempotency_key", idempotencyKey),
	)
	return s.repo.Topup(ctx, userID, amount, idempotencyKey)
}
