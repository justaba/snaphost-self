package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCredentials is the only answer a failed login gets. A wrong
// address and a wrong password are indistinguishable to the caller on purpose:
// this platform has one operator, and confirming which half was right is a free
// hint to anyone probing it.
var ErrInvalidCredentials = errors.New("invalid credentials")

// renewAfter is how much of a session's life must have elapsed before Verify
// extends it. A sliding expiry is what stops the operator being logged out
// mid-week, and this bound is what stops it becoming a database write on every
// authenticated request — SQLite has one writer, and the read path must not
// contend for it.
const renewAfter = time.Hour

// decoyHash is verified against when no account matches, so a login attempt
// costs the same whether or not the address exists. Computed once, from a
// password nobody holds.
var decoyHash = sync.OnceValue(func() string {
	password, err := GeneratePassword()
	if err != nil {
		// crypto/rand failing is not a condition to carry on from, but this is
		// package initialisation for a decoy: a constant that can never match
		// is still a correct decoy, and failing here would take down a process
		// that has not been asked to log anyone in yet.
		return "$argon2id$unusable"
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "$argon2id$unusable"
	}
	return hash
})

// Service is the login, logout and password-change logic, without HTTP.
type Service struct {
	repo *Repository
	ttl  time.Duration
}

// NewService creates the auth service. A non-positive ttl falls back to
// DefaultTTL.
func NewService(repo *Repository, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Service{repo: repo, ttl: ttl}
}

// TTL is how long a freshly issued session lives, which is also the cookie's
// max age.
func (s *Service) TTL() time.Duration { return s.ttl }

// Login verifies a password and issues a session, returning the plaintext token
// to put in the cookie.
func (s *Service) Login(ctx context.Context, email, password string) (string, *Session, error) {
	account, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, ErrNoAccount) {
			return "", nil, err
		}
		// Spend the same work on a miss as on a hit, so the response time does
		// not report whether the address exists.
		_ = VerifyPassword(decoyHash(), password)
		return "", nil, ErrInvalidCredentials
	}

	if err := VerifyPassword(account.PasswordHash, password); err != nil {
		return "", nil, ErrInvalidCredentials
	}

	now := time.Now()
	// Reclaim dead rows while already holding the write path. There is no
	// sweeper for this, and there does not need to be: an expired session is
	// already refused by the lookup, so all that is left to collect is disk.
	if err := s.repo.DeleteExpiredSessions(ctx, now); err != nil {
		return "", nil, err
	}

	token, hash, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	expires := now.Add(s.ttl)
	if err := s.repo.CreateSession(ctx, hash, account.ID, expires); err != nil {
		return "", nil, err
	}

	return token, &Session{
		UserID:    account.ID,
		Email:     account.Email,
		Role:      account.Role,
		ExpiresAt: expires,
	}, nil
}

// Verify resolves a presented token to the identity behind it, extending the
// session if it has been alive longer than renewAfter.
//
// An extension failure is not returned: the session is valid, the caller asked
// who is making the request, and a database hiccup while sliding an expiry must
// not become a 500 on an authenticated read.
func (s *Service) Verify(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	hash := HashToken(token)
	now := time.Now()

	session, err := s.repo.LookupSession(ctx, hash, now)
	if err != nil {
		return nil, err
	}

	if session.ExpiresAt.Sub(now) < s.ttl-renewAfter {
		extended := now.Add(s.ttl)
		if err := s.repo.ExtendSession(ctx, hash, extended); err == nil {
			session.ExpiresAt = extended
		}
	}
	return session, nil
}

// Logout revokes the presented session. An unknown token is not an error:
// logging out twice, or with a cookie the server has already forgotten, is the
// state the caller was asking for.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, HashToken(token))
}

// ChangePassword replaces an account's password after checking the current one,
// then revokes every session except the one that made the request.
//
// Revoking the others is the point of the operation as much as the new hash is:
// a password is changed because the old one may be somewhere it should not be,
// and a session issued under it outlives the password unless something says
// otherwise.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentToken, current, next string) error {
	account, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := VerifyPassword(account.PasswordHash, current); err != nil {
		return ErrInvalidCredentials
	}
	if err := CheckPasswordPolicy(next); err != nil {
		return err
	}

	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, userID, hash); err != nil {
		return err
	}

	keep := ""
	if currentToken != "" {
		keep = HashToken(currentToken)
	}
	return s.repo.DeleteUserSessionsExcept(ctx, userID, keep)
}
