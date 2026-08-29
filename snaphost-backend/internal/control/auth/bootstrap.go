package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RoleAdmin and RoleUser are the two roles the schema allows. The operator
// created at first start is an admin; everything else defaults to user.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Bootstrap creates the operator account the first time the platform starts,
// and prints its password exactly once.
//
// The password is generated, not defaulted. A shipped default is a credential
// every install shares and most never change, and this one authorises a panel
// that runs arbitrary containers on the host as root. It is written to the log
// because that is the only channel a freshly started container reliably has,
// and it is never written anywhere else — losing it means changing it, not
// reading it back.
//
// Subsequent starts do nothing: the condition is that no account can log in
// with a password at all, so a platform whose operator has changed their
// password, or their address, is not re-bootstrapped.
func Bootstrap(ctx context.Context, repo *Repository, email string, log *zap.Logger) error {
	existing, err := repo.CountPasswordAccounts(ctx)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	password, err := GeneratePassword()
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	// An account may already exist under this address without a password — a
	// row recorded before this platform issued its own identity. Promote it
	// rather than failing on the unique index, because failing would leave the
	// install with no way in.
	account, err := repo.FindByEmail(ctx, email)
	switch {
	case err == nil:
		if err := repo.SetPassword(ctx, account.ID, hash); err != nil {
			return err
		}
		if account.Role != RoleAdmin {
			if err := repo.setRole(ctx, account.ID, RoleAdmin); err != nil {
				return err
			}
		}
	case errors.Is(err, ErrNoAccount):
		if err := repo.CreateAccount(ctx, uuid.New(), email, hash, RoleAdmin); err != nil {
			return err
		}
	default:
		return err
	}

	// zap's production encoder writes JSON, so this arrives as one line with
	// the password as a field. It is printed rather than returned so that the
	// caller cannot accidentally log it twice.
	log.Warn("operator account created — this password is shown once and is not recoverable",
		zap.String("email", email),
		zap.String("password", password),
	)
	return nil
}

// setRole is only reachable from Bootstrap, which is why it is unexported: role
// is not something an HTTP surface changes today, and adding one is a decision
// with an audit trail attached to it.
func (r *Repository) setRole(ctx context.Context, userID uuid.UUID, role string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE users SET role = ? WHERE id = ?`, role, userID.String(),
	); err != nil {
		return fmt.Errorf("set role: %w", err)
	}
	return nil
}
