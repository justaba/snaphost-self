package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

// ErrNoAccount reports that no account matched. Login turns it into the same
// answer a wrong password gets, so it never reaches a client on its own.
var ErrNoAccount = errors.New("no such account")

// ErrNoSession reports an unknown, expired or revoked session token.
var ErrNoSession = errors.New("no such session")

// Account is one row of users, as authentication needs it.
type Account struct {
	ID           uuid.UUID
	Email        string
	Role         string
	PasswordHash string
}

// Session is a live session together with the identity behind it. The three
// fields are exactly what the middleware puts on the request, so verifying and
// enriching are one read rather than two.
type Session struct {
	UserID    uuid.UUID
	Email     string
	Role      string
	ExpiresAt time.Time
}

// Repository owns the users and sessions tables.
type Repository struct {
	db *sql.DB
}

// NewRepository creates an auth repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// CountPasswordAccounts returns how many accounts can log in with a password.
// Zero is what first start looks like, and it is the only condition under which
// the bootstrap creates one.
func (r *Repository) CountPasswordAccounts(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE password_hash IS NOT NULL AND password_hash <> ''`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count password accounts: %w", err)
	}
	return n, nil
}

// CreateAccount inserts an account with a password. It fails rather than
// upserting: the only caller is the bootstrap, and quietly overwriting an
// existing operator's password would be the worst possible way to recover from
// a duplicate.
func (r *Repository) CreateAccount(ctx context.Context, id uuid.UUID, email, passwordHash, role string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, role) VALUES (?, ?, ?, ?)`,
		id.String(), normalizeEmail(email), passwordHash, role,
	)
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// FindByEmail resolves a login address to an account. The comparison is on
// lower(email), which is what the unique index is built on, so it is both
// case-insensitive and indexed.
func (r *Repository) FindByEmail(ctx context.Context, email string) (*Account, error) {
	return r.scanAccount(r.db.QueryRowContext(ctx,
		`SELECT id, coalesce(email, ''), role, coalesce(password_hash, '')
		 FROM users WHERE lower(email) = ?`,
		normalizeEmail(email),
	))
}

// FindByID resolves an account by id.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*Account, error) {
	return r.scanAccount(r.db.QueryRowContext(ctx,
		`SELECT id, coalesce(email, ''), role, coalesce(password_hash, '')
		 FROM users WHERE id = ?`,
		id.String(),
	))
}

func (r *Repository) scanAccount(row *sql.Row) (*Account, error) {
	var a Account
	if err := row.Scan(&a.ID, &a.Email, &a.Role, &a.PasswordHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoAccount
		}
		return nil, fmt.Errorf("load account: %w", err)
	}
	return &a, nil
}

// SetPassword replaces an account's password hash.
func (r *Repository) SetPassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`,
		passwordHash, userID.String(),
	)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n == 0 {
		return ErrNoAccount
	}
	return nil
}

// CreateSession stores a session under the hash of its token.
func (r *Repository) CreateSession(ctx context.Context, tokenHash string, userID uuid.UUID, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID.String(), controldb.FormatTime(expiresAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// LookupSession returns the session behind a token hash, together with the
// identity it authenticates. An expired row answers ErrNoSession and is left in
// place for the next login's sweep to remove — deleting it here would put a
// write on the read path.
//
// The comparison is a string one against an RFC 3339 UTC value formatted in Go.
// SQLite's own datetime('now') would render 'YYYY-MM-DD HH:MM:SS', whose space
// sorts before every digit, and every session would read as live.
func (r *Repository) LookupSession(ctx context.Context, tokenHash string, now time.Time) (*Session, error) {
	var (
		s        Session
		expires  string
		rawEmail sql.NullString
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT s.user_id, u.email, u.role, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, controldb.FormatTime(now),
	).Scan(&s.UserID, &rawEmail, &s.Role, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoSession
		}
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	s.Email = rawEmail.String

	s.ExpiresAt, err = controldb.ParseTime(expires)
	if err != nil {
		return nil, fmt.Errorf("lookup session: parse expires_at %q: %w", expires, err)
	}
	return &s, nil
}

// ExtendSession pushes a session's expiry out. The caller decides how rarely
// this happens; see Service.Verify.
func (r *Repository) ExtendSession(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
		controldb.FormatTime(expiresAt), tokenHash,
	)
	if err != nil {
		return fmt.Errorf("extend session: %w", err)
	}
	return nil
}

// DeleteSession revokes one session. Logout.
func (r *Repository) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessionsExcept revokes every session an account has except the one
// presented. It is what a password change runs, so the browser doing the
// changing stays logged in and every other one does not.
//
// keepHash may be empty, which revokes all of them.
func (r *Repository) DeleteUserSessionsExcept(ctx context.Context, userID uuid.UUID, keepHash string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`,
		userID.String(), keepHash,
	)
	if err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes rows past their expiry. Called on login rather
// than from a ticker: expired rows are already unusable, so the only thing left
// to reclaim is disk, and login is both rare and already writing.
func (r *Repository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, controldb.FormatTime(now),
	); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

// normalizeEmail folds an address for storage and lookup. Both sides go through
// this, so the unique index on lower(email) and FindByEmail agree.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
