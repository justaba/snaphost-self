package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"snaphost/internal/ai/cache"
	"snaphost/internal/ai/config"
	"snaphost/internal/ai/llm"
	"snaphost/internal/ai/usage"
	controldb "snaphost/internal/control/db"
)

// A cached template render outlives the binary that produced it, and the
// template library lives inside that binary. So upgrading the platform — the
// one way a broken template gets fixed — does not reach a repository that has
// been deployed before: the cache keeps serving the old Dockerfile until its
// TTL runs out.
//
// This is what happened. The four nginx templates wrote their config in a way
// the read-only root filesystem refused, the fix shipped, and the very next
// deploy of the same repository failed in exactly the same way, from cache.
//
// The cache is for LLM answers, which cost money and time. A template render
// is local and deterministic, so there is nothing to save by keeping one.
func TestATemplateRenderIsNeitherServedNorStoredFromCache(t *testing.T) {
	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	cacheRepo := cache.NewRepository(handle)
	svc := New(cacheRepo, usage.NewRepository(handle), nil, &config.Config{}, zap.NewNop())

	req := llm.GenerateRequest{
		DeployID: "3ac4abd0-1316-4995-ad21-e79658db017e",
		UserID:   "0fc297d3-f54d-4e73-9cae-3b3ded1167c4",
		FileTree: []string{"index.html"},
	}

	const poisoned = "FROM scratch\n# the previous release's broken template\n"
	signature := cacheRepo.ComputeSignature(req)
	if err := cacheRepo.Store(context.Background(), signature, poisoned, 8080, "static-nginx", "template"); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	resp, err := svc.GenerateDockerfile(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateDockerfile() error = %v", err)
	}

	if strings.Contains(resp.Dockerfile, "the previous release") {
		t.Fatal("a template render was served from cache; an upgraded template would never reach a repository deployed before it")
	}
	if resp.CacheHit {
		t.Fatalf("CacheHit = true for source %q", resp.Source)
	}

	// And it must not have written itself back, or the next call reads it.
	cached, err := cacheRepo.Get(context.Background(), signature)
	if err == nil && cached.Dockerfile == resp.Dockerfile {
		t.Fatal("the template render was stored in the cache it must not enter")
	}
}
