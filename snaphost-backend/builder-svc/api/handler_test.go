package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/builder-svc/config"
)

const testSecret = "test-webhook-secret"

// newBuildRouter wires a handler with a nil queue — every test case here
// must be rejected by validation before the queue is touched.
func newBuildRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewHandler(nil, &config.Config{WebhookSecret: testSecret}, zap.NewNop())
	r := gin.New()
	r.POST("/api/v1/build", h.Build)
	return r
}

func TestBuild_SourceValidationMatrix(t *testing.T) {
	const ids = `"deploy_id":"b132cb0d-ce92-4009-a8f3-221d86d8607c","user_id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"`

	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "unknown source type",
			body:    `{` + ids + `,"source_type":"svn","repo_url":"https://github.com/a/b","branch":"main"}`,
			wantErr: "invalid_source_type",
		},
		{
			name:    "default git_public missing repo_url",
			body:    `{` + ids + `,"branch":"main"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "git_public missing branch",
			body:    `{` + ids + `,"repo_url":"https://github.com/a/b"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "git_public with upload_id",
			body:    `{` + ids + `,"repo_url":"https://github.com/a/b","branch":"main","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "archive missing upload_id",
			body:    `{` + ids + `,"source_type":"archive"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "archive with repo_url",
			body:    `{` + ids + `,"source_type":"archive","repo_url":"https://github.com/a/b","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "archive malformed upload_id",
			body:    `{` + ids + `,"source_type":"archive","upload_id":"nope"}`,
			wantErr: "invalid_upload_id",
		},
		{
			name:    "git_private missing credential_id",
			body:    `{` + ids + `,"source_type":"git_private","repo_url":"https://github.com/a/b","branch":"main"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "git_private malformed credential_id",
			body:    `{` + ids + `,"source_type":"git_private","repo_url":"https://github.com/a/b","branch":"main","credential_id":"nope"}`,
			wantErr: "invalid_credential_id",
		},
		{
			name:    "git_public with credential_id",
			body:    `{` + ids + `,"repo_url":"https://github.com/a/b","branch":"main","credential_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "archive with credential_id",
			body:    `{` + ids + `,"source_type":"archive","upload_id":"3b241101-e2bb-4255-8caf-4136c566a962","credential_id":"3b241101-e2bb-4255-8caf-4136c566a962"}`,
			wantErr: "invalid_body",
		},
		{
			name:    "credentials embedded in url",
			body:    `{` + ids + `,"repo_url":"https://user:pass@github.com/a/b","branch":"main"}`,
			wantErr: "invalid_repo_url",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newBuildRouter()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/build", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Webhook-Secret", testSecret)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400; body=%s", w.Code, w.Body.String())
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

func TestBuild_RejectsBadWebhookSecret(t *testing.T) {
	r := newBuildRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", "wrong")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", w.Code, w.Body.String())
	}
}
