package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/internal/control/saga"
	"snaphost/internal/uploads"
)

// fakeRepo is a minimal Repo for handler-level testing. Only methods
// exercised by the tests in this file are non-nil; others panic on use.
type fakeRepo struct {
	getFn func(ctx context.Context, id uuid.UUID) (*Deploy, error)
}

func (f *fakeRepo) Get(ctx context.Context, id uuid.UUID) (*Deploy, error) {
	return f.getFn(ctx, id)
}

func (f *fakeRepo) Create(context.Context, Deploy) error { panic("not used") }
func (f *fakeRepo) ListByUser(context.Context, uuid.UUID, int, int) ([]Deploy, error) {
	panic("not used")
}
func (f *fakeRepo) MarkDeleted(context.Context, uuid.UUID) error { panic("not used") }
func (f *fakeRepo) BeginRestart(context.Context, uuid.UUID, int) (*RestartTarget, error) {
	panic("not used")
}
func (f *fakeRepo) AbandonRestart(context.Context, uuid.UUID, string) error { panic("not used") }
func (f *fakeRepo) GetSaga(context.Context, uuid.UUID) (*SagaView, error)   { return nil, nil }

// newCreateDeployRouter wires a handler whose saga queue is non-nil (so
// the "saga disabled" guard passes) but never reached — every test case
// here must fail validation or be gated before repo/queue use.
func newCreateDeployRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{
		repo: &fakeRepo{
			getFn: func(context.Context, uuid.UUID) (*Deploy, error) {
				t.Fatal("repo must not be reached")
				return nil, nil
			},
		},
		log:       zap.NewNop(),
		sagaQueue: saga.NewQueue(0, zap.NewNop()),
	}
	r := gin.New()
	r.POST("/api/v1/deploys", h.CreateDeploy)
	return r
}

func TestCreateDeploy_SourceValidationMatrix(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string
	}{
		{
			name:     "unknown source type",
			body:     `{"source_type":"svn","repo_url":"https://github.com/a/b","branch":"main"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_source_type",
		},
		{
			name:     "default git_public missing repo_url",
			body:     `{"branch":"main"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "git_public missing branch",
			body:     `{"source_type":"git_public","repo_url":"https://github.com/a/b"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "git_public malformed repo_url",
			body:     `{"repo_url":"not a url","branch":"main"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_repo_url",
		},
		{
			name:     "git_public with upload_id",
			body:     `{"repo_url":"https://github.com/a/b","branch":"main","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "archive missing upload_id",
			body:     `{"source_type":"archive"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "archive with repo_url",
			body:     `{"source_type":"archive","repo_url":"https://github.com/a/b","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "archive malformed upload_id",
			body:     `{"source_type":"archive","upload_id":"not-a-uuid"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_upload_id",
		},
		{
			// The test handler has no upload store wired, so a
			// structurally valid archive request stops at the
			// uploads-disabled guard — before any repo/queue use.
			name:     "archive requires upload store",
			body:     `{"source_type":"archive","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantCode: http.StatusServiceUnavailable,
			wantErr:  "uploads_disabled",
		},
		{
			name:     "git_private missing token",
			body:     `{"source_type":"git_private","repo_url":"https://github.com/a/b","branch":"main"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			// The test handler has no credential store wired, so a valid
			// git_private request stops at the disabled guard.
			name:     "git_private requires credential store",
			body:     `{"source_type":"git_private","repo_url":"https://github.com/a/b","branch":"main","git_token":"ghp_x"}`,
			wantCode: http.StatusServiceUnavailable,
			wantErr:  "credentials_disabled",
		},
		{
			name:     "git_public with token",
			body:     `{"repo_url":"https://github.com/a/b","branch":"main","git_token":"ghp_x"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
		{
			name:     "credentials embedded in url",
			body:     `{"repo_url":"https://user:pass@github.com/a/b","branch":"main"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_repo_url",
		},
		{
			name:     "archive with token",
			body:     `{"source_type":"archive","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962","git_token":"ghp_x"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateDeployRouter(t)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/deploys", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-ID", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
			r.ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("status: got %d, want %d; body=%s", w.Code, tc.wantCode, w.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["error"] != tc.wantErr {
				t.Errorf("error code: got %q, want %q; body=%s", body["error"], tc.wantErr, w.Body.String())
			}
		})
	}
}

func newUploadRouter(t *testing.T, maxBytes int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{
		log: zap.NewNop(),
		// Non-nil store so the disabled-guard passes; Put is never
		// reached because every case below fails validation first.
		uploads:        newTestUploadStore(t),
		maxUploadBytes: maxBytes,
		uploadTTL:      time.Minute,
	}
	r := gin.New()
	r.POST("/api/v1/deploys/upload", h.UploadArchive)
	return r
}

func TestUploadArchive_Validation(t *testing.T) {
	gzipMagic := "\x1f\x8b"

	cases := []struct {
		name     string
		body     string
		userID   string
		wantCode int
	}{
		{"missing user header", gzipMagic + "data", "", http.StatusUnauthorized},
		{"not gzip", "PK\x03\x04 zip not tar.gz", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", http.StatusBadRequest},
		{"empty body", "", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", http.StatusBadRequest},
		{"too large", gzipMagic + strings.Repeat("a", 100), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newUploadRouter(t, 64)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/deploys/upload", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/gzip")
			if tc.userID != "" {
				req.Header.Set("X-User-ID", tc.userID)
			}
			r.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("status: got %d, want %d; body=%s", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

func TestUploadArchive_DisabledWithoutAStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{log: zap.NewNop()}
	r := gin.New()
	r.POST("/api/v1/deploys/upload", h.UploadArchive)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deploys/upload", strings.NewReader("x"))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", w.Code)
	}
}

func TestCreateDeployRequest_ValidateDefaultsToGitPublic(t *testing.T) {
	req := CreateDeployRequest{RepoURL: "https://github.com/a/b", Branch: "main"}
	if code, msg := req.validate(); code != "" {
		t.Fatalf("validate: got %q/%q, want ok", code, msg)
	}
	if req.SourceType != SourceGitPublic {
		t.Errorf("SourceType: got %q, want %q", req.SourceType, SourceGitPublic)
	}
}

// --- client-supplied project key ------------------------------------------

func TestCreateDeployRequest_AcceptsAProjectKey(t *testing.T) {
	req := CreateDeployRequest{
		SourceType: SourceArchive,
		UploadID:   "3f2a1c88-1e2b-4a5c-9f00-1122334455aa",
		ProjectKey: "workspace/my-app",
	}
	if code, msg := req.validate(); code != "" {
		t.Fatalf("validate: got %q/%q, want ok", code, msg)
	}
}

func TestCreateDeployRequest_RejectsAMalformedProjectKey(t *testing.T) {
	for _, key := range []string{"has space", "emoji-🙂", strings.Repeat("k", 129)} {
		req := CreateDeployRequest{
			SourceType: SourceArchive,
			UploadID:   "3f2a1c88-1e2b-4a5c-9f00-1122334455aa",
			ProjectKey: key,
		}
		code, _ := req.validate()
		if code != "invalid_project_key" {
			t.Errorf("validate(%q): got %q, want invalid_project_key", key, code)
		}
	}
}

// Omitting the key must keep the pre-existing request shape working, so
// nothing that deploys today starts failing.
func TestCreateDeployRequest_ProjectKeyIsOptional(t *testing.T) {
	req := CreateDeployRequest{
		SourceType: SourceArchive,
		UploadID:   "3f2a1c88-1e2b-4a5c-9f00-1122334455aa",
	}
	if code, msg := req.validate(); code != "" {
		t.Fatalf("validate: got %q/%q, want ok", code, msg)
	}
}

// newTestUploadStore backs the upload handler with a real directory under the
// test's temp dir. The store used to be constructible over a nil Redis client
// because nothing in these tests reached it; a file-backed one has to exist.
func newTestUploadStore(t *testing.T) *uploads.Store {
	t.Helper()

	store, err := uploads.NewStore(t.TempDir(), time.Minute)
	if err != nil {
		t.Fatalf("upload store: %v", err)
	}
	return store
}
