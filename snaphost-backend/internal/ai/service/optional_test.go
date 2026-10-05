package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/ai/cache"
	"snaphost/internal/ai/config"
	"snaphost/internal/ai/llm"
	"snaphost/internal/ai/usage"
	controldb "snaphost/internal/control/db"
)

// A real HTTP client and a local provider prove both that opt-out makes zero
// requests and that opt-in still generates and caches a Dockerfile.
func TestOptionalProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		template bool
		cached   bool
	}{
		{name: "disabled unsupported project"},
		{name: "disabled template project", template: true},
		{name: "disabled ignores old AI cache", cached: true},
		{name: "disabled template wins over old AI cache", template: true, cached: true},
		{name: "enabled template avoids provider", enabled: true, template: true},
		{name: "enabled unsupported project calls provider", enabled: true},
		{name: "enabled reuses AI cache", enabled: true, cached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			const generated = "FROM nginx:1.27-alpine\nEXPOSE 8080\n"
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("provider request path or authentication is incorrect")
				}
				content, err := json.Marshal(llm.GenerateResponse{Dockerfile: generated, ExposePort: 8080})
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"model":   "test-model",
					"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(content)}}},
				}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(provider.Close)
			handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "ai.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
				t.Fatal(err)
			}
			cacheRepo := cache.NewRepository(handle)
			client := llm.NewClient(llm.Config{APIKey: "test-key", BaseURL: provider.URL, Model: "test-model", Timeout: time.Second}, zap.NewNop())
			svc := New(cacheRepo, usage.NewRepository(handle), llm.NewCircuitClient(client, 10, time.Second), &config.Config{LLMEnabled: tc.enabled}, zap.NewNop())
			req := llm.GenerateRequest{FileTree: []string{"README.md"}}
			if tc.template {
				req.FileTree = []string{"index.html"}
			}
			if tc.cached {
				if err := cacheRepo.Store(context.Background(), cacheRepo.ComputeSignature(req), generated, 8080, "llm-generated", "llm"); err != nil {
					t.Fatal(err)
				}
			}
			resp, err := svc.GenerateDockerfile(context.Background(), req)
			switch {
			case !tc.enabled && !tc.template:
				if !errors.Is(err, ErrLLMDisabled) {
					t.Fatalf("unsupported project error = %v, want ErrLLMDisabled", err)
				}
			case tc.template:
				if err != nil || resp.Source != "template" || resp.Dockerfile == "" {
					t.Fatalf("template response = %+v, error = %v", resp, err)
				}
			default:
				if err != nil || resp.Source != "llm" || resp.Dockerfile != generated {
					t.Fatalf("provider response = %+v, error = %v", resp, err)
				}
				// A second attempt must use the stored response without another call.
				resp, err = svc.GenerateDockerfile(context.Background(), req)
				if err != nil || !resp.CacheHit {
					t.Fatalf("cache response = %+v, error = %v", resp, err)
				}
			}
			var wantCalls int32
			if tc.enabled && !tc.template && !tc.cached {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("provider calls = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}
