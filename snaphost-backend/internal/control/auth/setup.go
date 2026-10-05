package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

var (
	ErrSetupComplete = errors.New("operator account is already configured")
	ErrSetupToken    = errors.New("invalid setup token")
	ErrInvalidLogin  = errors.New("login must contain 1 to 254 characters without whitespace or control characters")
)

// Setup authorizes exactly one operator account using a host-owned secret.
// Existing password accounts keep their identity and never reopen setup.
type Setup struct {
	repo      *Repository
	tokenPath string
	tokenHash string
}

// PrepareSetup persists a random setup token outside logs and the database.
// The installer reads this protected file to print a private URL fragment.
func PrepareSetup(ctx context.Context, repo *Repository, tokenPath string) (*Setup, error) {
	s := &Setup{repo: repo, tokenPath: tokenPath}
	required, err := s.Required(ctx)
	if err != nil {
		return nil, err
	}
	if !required {
		// A crash after account creation may leave this file. It is unusable
		// once a password account exists, even if removal fails.
		_ = os.Remove(tokenPath)
		return s, nil
	}

	dir := filepath.Dir(tokenPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create operator setup directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, errors.New("operator setup directory must be a real directory with mode 0700")
	}
	if _, err := os.Lstat(tokenPath); errors.Is(err, os.ErrNotExist) {
		token, _, err := NewToken()
		if err != nil {
			return nil, err
		}
		if err := publishSetupToken(tokenPath, token); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect setup token: %w", err)
	}
	info, err = os.Lstat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("setup token must be a regular file with mode 0600")
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read setup token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != sessionTokenBytes {
		return nil, errors.New("setup token file is invalid")
	}
	s.tokenHash = HashToken(token)
	return s, nil
}

func publishSetupToken(path, token string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".setup-token-")
	if err != nil {
		return fmt.Errorf("create setup token: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("write setup token: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync setup token: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close setup token: %w", err)
	}
	// A hard link publishes the complete file atomically without overwriting
	// a token another starting process may already have published.
	if err := os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("publish setup token: %w", err)
	}
	return nil
}

func (s *Setup) Required(ctx context.Context) (bool, error) {
	n, err := s.repo.CountPasswordAccounts(ctx)
	return n == 0, err
}

func (s *Setup) Complete(ctx context.Context, token, login, password string) error {
	required, err := s.Required(ctx)
	if err != nil {
		return err
	}
	if !required {
		return ErrSetupComplete
	}
	if s.tokenHash == "" || subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(s.tokenHash)) != 1 {
		return ErrSetupToken
	}
	login = normalizeEmail(login)
	if login == "" || len(login) > 254 || strings.ContainsFunc(login, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return ErrInvalidLogin
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repo.CompleteSetup(ctx, login, hash); err != nil {
		return err
	}
	_ = os.Remove(s.tokenPath)
	return nil
}
