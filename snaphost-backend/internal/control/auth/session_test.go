package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	controldb "snaphost/internal/control/db"
)

// These run against a real SQLite file, because the failures worth catching
// here are the ones a fake repository cannot have: a timestamp comparison that
// silently matches every row, a unique index that does not exist, a scan order
// that drifted from its SELECT list.
func testRepo(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })

	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return NewRepository(handle), handle
}

const testEmail = "operator@example.test"

// bootstrapped runs the first-start path and returns the password it printed,
// which is the only place that value exists.
func bootstrapped(t *testing.T, repo *Repository) string {
	t.Helper()

	core, logs := observer.New(zapcore.WarnLevel)
	if err := Bootstrap(context.Background(), repo, testEmail, zap.New(core)); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("Bootstrap logged %d entries, want exactly 1", len(entries))
	}
	for _, field := range entries[0].Context {
		if field.Key == "password" {
			return field.String
		}
	}
	t.Fatal("Bootstrap logged no password")
	return ""
}

func TestBootstrapCreatesAnAdminOnce(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)

	account, err := repo.FindByEmail(context.Background(), testEmail)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if account.Role != RoleAdmin {
		t.Errorf("role = %q, want %q — the operator cannot reach the admin console", account.Role, RoleAdmin)
	}
	if err := VerifyPassword(account.PasswordHash, password); err != nil {
		t.Errorf("the printed password does not verify against the stored hash: %v", err)
	}
	if strings.Contains(account.PasswordHash, password) {
		t.Error("the stored hash contains the plaintext password")
	}

	// A second start must not print another password or replace the first.
	core, logs := observer.New(zapcore.WarnLevel)
	if err := Bootstrap(context.Background(), repo, testEmail, zap.New(core)); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if n := logs.Len(); n != 0 {
		t.Fatalf("second Bootstrap logged %d entries, want 0", n)
	}

	again, err := repo.FindByEmail(context.Background(), testEmail)
	if err != nil {
		t.Fatalf("FindByEmail after second bootstrap: %v", err)
	}
	if again.PasswordHash != account.PasswordHash {
		t.Error("a restart replaced the operator's password")
	}
}

// A row recorded before this platform issued its own identity has an address
// and no password. Bootstrap must adopt it rather than fail on the unique
// index, because failing leaves the install with no way in.
func TestBootstrapAdoptsAPasswordlessRow(t *testing.T) {
	repo, handle := testRepo(t)

	if _, err := handle.Exec(
		`INSERT INTO users (id, email) VALUES ('11111111-2222-4333-8444-555555555555', ?)`, testEmail,
	); err != nil {
		t.Fatalf("seed passwordless user: %v", err)
	}

	password := bootstrapped(t, repo)

	account, err := repo.FindByEmail(context.Background(), testEmail)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if account.ID.String() != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("a second row was created: id = %s", account.ID)
	}
	if account.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", account.Role, RoleAdmin)
	}
	if err := VerifyPassword(account.PasswordHash, password); err != nil {
		t.Errorf("adopted row does not accept the printed password: %v", err)
	}
}

func TestLoginIssuesAVerifiableSession(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)
	ctx := context.Background()

	token, session, err := svc.Login(ctx, testEmail, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Role != RoleAdmin || session.Email != testEmail {
		t.Fatalf("session = %+v, want the operator's identity", session)
	}

	verified, err := svc.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.UserID != session.UserID || verified.Role != RoleAdmin {
		t.Fatalf("verified = %+v, want the same identity Login returned", verified)
	}
}

// The address is folded on both sides, so the case an operator types is not the
// case the account was created with.
func TestLoginIsCaseInsensitiveOnTheAddress(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)

	if _, _, err := svc.Login(context.Background(), "  OPERATOR@Example.TEST ", password); err != nil {
		t.Fatalf("Login with a differently cased address: %v", err)
	}
}

func TestLoginRefusesAWrongPasswordAndAnUnknownAddress(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)
	ctx := context.Background()

	if _, _, err := svc.Login(ctx, testEmail, password+"x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password = %v, want ErrInvalidCredentials", err)
	}
	if _, _, err := svc.Login(ctx, "nobody@example.test", password); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown address = %v, want ErrInvalidCredentials", err)
	}
}

// The trap this whole schema is built around. expires_at is TEXT and the
// comparison is string ordering, so a value written in any other format — or
// compared against SQLite's own datetime('now'), whose space sorts before every
// digit — makes every session read as live.
func TestExpiredSessionsAreRefused(t *testing.T) {
	repo, _ := testRepo(t)
	bootstrapped(t, repo)
	ctx := context.Background()

	account, err := repo.FindByEmail(ctx, testEmail)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	token, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, hash, account.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc := NewService(repo, time.Hour)
	if _, err := svc.Verify(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Verify on an expired session = %v, want ErrNoSession", err)
	}
}

// A login sweeps rows that are already unusable. Nothing else does, so if this
// stops working the table grows forever.
func TestLoginReclaimsExpiredSessions(t *testing.T) {
	repo, handle := testRepo(t)
	password := bootstrapped(t, repo)
	ctx := context.Background()

	account, err := repo.FindByEmail(ctx, testEmail)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if err := repo.CreateSession(ctx, "dead-session", account.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc := NewService(repo, time.Hour)
	if _, _, err := svc.Login(ctx, testEmail, password); err != nil {
		t.Fatalf("Login: %v", err)
	}

	var remaining int
	if err := handle.QueryRow(`SELECT count(*) FROM sessions WHERE token_hash = 'dead-session'`).Scan(&remaining); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if remaining != 0 {
		t.Error("login left an expired session in the table")
	}
}

func TestLogoutRevokes(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)
	ctx := context.Background()

	token, _, err := svc.Login(ctx, testEmail, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Verify(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Verify after logout = %v, want ErrNoSession", err)
	}

	// Logging out twice is what the caller asked for, not an error.
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("second Logout: %v", err)
	}
}

// This is half the reason sessions are a table. A password is changed because
// the old one may be somewhere it should not be, so every session issued under
// it has to stop working — except the one doing the changing.
func TestChangingThePasswordRevokesEveryOtherSession(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)
	ctx := context.Background()

	keptToken, session, err := svc.Login(ctx, testEmail, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	otherToken, _, err := svc.Login(ctx, testEmail, password)
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	const next = "a new operator password"
	if err := svc.ChangePassword(ctx, session.UserID, keptToken, password, next); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := svc.Verify(ctx, keptToken); err != nil {
		t.Errorf("the session that changed the password was revoked: %v", err)
	}
	if _, err := svc.Verify(ctx, otherToken); !errors.Is(err, ErrNoSession) {
		t.Errorf("another session survived the password change: %v", err)
	}

	if _, _, err := svc.Login(ctx, testEmail, password); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("the old password still works")
	}
	if _, _, err := svc.Login(ctx, testEmail, next); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestChangePasswordChecksTheCurrentOneAndThePolicy(t *testing.T) {
	repo, _ := testRepo(t)
	password := bootstrapped(t, repo)
	svc := NewService(repo, time.Hour)
	ctx := context.Background()

	token, session, err := svc.Login(ctx, testEmail, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := svc.ChangePassword(ctx, session.UserID, token, "not the password", "a new operator password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong current password = %v, want ErrInvalidCredentials", err)
	}
	if err := svc.ChangePassword(ctx, session.UserID, token, password, "short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short new password = %v, want ErrPasswordTooShort", err)
	}
	if _, _, err := svc.Login(ctx, testEmail, password); err != nil {
		t.Errorf("a refused change altered the password anyway: %v", err)
	}
}

// A session used past renewAfter slides forward, so an operator who visits the
// panel every day is never logged out. One that has barely been issued is left
// alone, which is what keeps this off the write path.
func TestVerifySlidesTheExpiryOnlyAfterRenewAfter(t *testing.T) {
	repo, _ := testRepo(t)
	bootstrapped(t, repo)
	ctx := context.Background()

	account, err := repo.FindByEmail(ctx, testEmail)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	const ttl = 24 * time.Hour
	svc := NewService(repo, ttl)

	fresh, freshHash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	freshExpiry := time.Now().Add(ttl)
	if err := repo.CreateSession(ctx, freshHash, account.ID, freshExpiry); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stale, staleHash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	staleExpiry := time.Now().Add(ttl - 3*time.Hour)
	if err := repo.CreateSession(ctx, staleHash, account.ID, staleExpiry); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := svc.Verify(ctx, fresh); err != nil {
		t.Fatalf("Verify fresh: %v", err)
	}
	if _, err := svc.Verify(ctx, stale); err != nil {
		t.Fatalf("Verify stale: %v", err)
	}

	read := func(hash string) time.Time {
		t.Helper()
		var raw string
		if err := repo.db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash = ?`, hash).Scan(&raw); err != nil {
			t.Fatalf("read expires_at: %v", err)
		}
		parsed, err := controldb.ParseTime(raw)
		if err != nil {
			t.Fatalf("parse expires_at %q: %v", raw, err)
		}
		return parsed
	}

	if got := read(freshHash); got.Sub(freshExpiry).Abs() > time.Second {
		t.Errorf("a freshly issued session was rewritten: %v -> %v", freshExpiry, got)
	}
	if got := read(staleHash); !got.After(staleExpiry.Add(time.Minute)) {
		t.Errorf("a session past renewAfter was not extended: %v -> %v", staleExpiry, got)
	}
}
