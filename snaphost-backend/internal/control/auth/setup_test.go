package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func pendingSetup(t *testing.T, repo *Repository) (*Setup, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator-setup", "token")
	s, err := PrepareSetup(context.Background(), repo, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, strings.TrimSpace(string(data))
}

func TestSetupWaitsForChosenCredentialsAndSurvivesRestart(t *testing.T) {
	repo, _ := testRepo(t)
	s, token := pendingSetup(t, repo)
	ctx := context.Background()
	if n, err := repo.CountPasswordAccounts(ctx); err != nil || n != 0 {
		t.Fatalf("startup created a password account: count=%d err=%v", n, err)
	}
	for path, mode := range map[string]os.FileMode{s.tokenPath: 0o600, filepath.Dir(s.tokenPath): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("setup secret permissions differ from the protected contract")
		}
	}
	restarted, err := PrepareSetup(ctx, repo, s.tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.tokenHash != s.tokenHash {
		t.Fatal("restart replaced the pending token")
	}
	const password = "my own operator password"
	if err := restarted.Complete(ctx, token, "  BORIS  ", password); err != nil {
		t.Fatal(err)
	}
	account, err := repo.FindByEmail(ctx, "boris")
	if err != nil || account.Role != RoleAdmin || account.Email != "boris" {
		t.Fatalf("operator identity not created correctly: %v", err)
	}
	if VerifyPassword(account.PasswordHash, password) != nil || strings.Contains(account.PasswordHash, password) {
		t.Fatal("chosen password was not stored as a usable hash")
	}
	if _, err := os.Stat(s.tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed setup left its plaintext token")
	}
	if err := s.Complete(ctx, token, "other", "a different password"); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("replayed setup was not refused: %v", err)
	}
	closed, err := PrepareSetup(ctx, repo, s.tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if required, err := closed.Required(ctx); err != nil || required || closed.tokenHash != "" {
		t.Fatal("restart reopened completed setup")
	}
	if _, err := os.Stat(s.tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart minted another setup token")
	}
}

func TestSetupRefusesMissingTokenAndInvalidCredentials(t *testing.T) {
	repo, _ := testRepo(t)
	s, token := pendingSetup(t, repo)
	ctx := context.Background()
	for _, supplied := range []string{"", "wrong"} {
		if err := s.Complete(ctx, supplied, "boris", "chosen password here"); !errors.Is(err, ErrSetupToken) {
			t.Fatalf("invalid token accepted: %v", err)
		}
	}
	for _, login := range []string{"", "two words", "a\x00b", strings.Repeat("a", 255)} {
		if err := s.Complete(ctx, token, login, "chosen password here"); !errors.Is(err, ErrInvalidLogin) {
			t.Fatalf("invalid login accepted: %v", err)
		}
	}
	if err := s.Complete(ctx, token, "boris", "short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("weak password accepted: %v", err)
	}
	if n, _ := repo.CountPasswordAccounts(ctx); n != 0 {
		t.Fatal("refused setup wrote an account")
	}
}

func TestConcurrentSetupCreatesExactlyOneOperator(t *testing.T) {
	repo, _ := testRepo(t)
	s, token := pendingSetup(t, repo)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.Complete(context.Background(), token, name, "chosen password here")
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSetupComplete):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if n, _ := repo.CountPasswordAccounts(context.Background()); n != 1 || successes != 1 || conflicts != 1 {
		t.Fatal("concurrent setup did not have exactly one winner")
	}
}

func TestSetupAdoptsMatchingPasswordlessAccount(t *testing.T) {
	repo, handle := testRepo(t)
	const id = "11111111-2222-4333-8444-555555555555"
	if _, err := handle.Exec(`INSERT INTO users (id,email) VALUES (?,?)`, id, testEmail); err != nil {
		t.Fatal(err)
	}
	s, token := pendingSetup(t, repo)
	if err := s.Complete(context.Background(), token, testEmail, "chosen password here"); err != nil {
		t.Fatal(err)
	}
	account, err := repo.FindByEmail(context.Background(), testEmail)
	if err != nil || account.ID.String() != id || account.Role != RoleAdmin {
		t.Fatal("setup failed to retain the matching legacy user identity")
	}
}

func TestSetupRefusesInsecureOrSymlinkToken(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable", true: "symlink"}[symlink], func(t *testing.T) {
			repo, _ := testRepo(t)
			s, _ := pendingSetup(t, repo)
			if symlink {
				target := s.tokenPath + ".saved"
				if err := os.Rename(s.tokenPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, s.tokenPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(s.tokenPath, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := PrepareSetup(context.Background(), repo, s.tokenPath); err == nil {
				t.Fatal("unsafe setup token was accepted")
			}
		})
	}
}

func TestSetupHTTPFlowIssuesSessionAndCloses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, db := testRepo(t)
	s, token := pendingSetup(t, repo)
	router := gin.New()
	handler := NewHandler(NewService(repo, time.Hour), CookieOptions{}, zap.NewNop(), s)
	handler.Register(router.Group("/api/v1"))
	request := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/auth/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		router.ServeHTTP(w, req)
		return w
	}
	if w := request(http.MethodGet, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"required":true`) || strings.Contains(w.Body.String(), token) {
		t.Fatal("setup status leaked its secret or failed")
	}
	if w := request(http.MethodPost, `{"email":"boris","password":"chosen password here"}`); w.Code != 403 {
		t.Fatalf("unauthorized setup answered %d", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"email": "boris", "password": "chosen password here", "token": token})
	w := request(http.MethodPost, string(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("authorized setup answered %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("setup did not issue a protected HTTPS session")
	}
	if _, err := NewService(repo, time.Hour).Verify(context.Background(), cookies[0].Value); err != nil {
		t.Fatal("setup cookie does not authenticate")
	}
	if w := request(http.MethodPost, string(body)); w.Code != http.StatusConflict {
		t.Fatal("HTTP setup replay was accepted")
	}
	if w := request(http.MethodGet, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"required":false`) {
		t.Fatal("completed setup status is still open")
	}
	_ = db.Close()
	if w := request(http.MethodGet, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatal("database failure did not fail closed")
	}
}
