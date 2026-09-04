package edge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

func newEdgeRepository(t *testing.T) (*Repository, string) {
	t.Helper()
	db, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := controldb.RunMigrations(db, zap.NewNop()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	const (
		userID    = "4e7a27e0-a31a-4ce2-80bd-7cd29c17bf39"
		projectID = "b17fd46d-577d-4a2e-b1cf-32272c423d82"
		deployID  = "137f946f-224b-4c14-8d92-c72b743a86d0"
	)
	if _, err := db.Exec(`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, 'site', 'git:site')`, projectID, userID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO deploys (id, user_id, project_id, source_type, status, subdomain, container_id)
		VALUES (?, ?, ?, 'git_public', 'running', 'proj-live', 'container-id')`, deployID, userID, projectID); err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, app_port)
		VALUES (?, ?, 'running', 4173)`, deployID, userID); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO custom_domains
		(id, user_id, project_id, target_deploy_id, domain, verification_token, status)
		VALUES ('e08b2d5b-79fb-4887-a159-068ae1fb7241', ?, ?, ?, 'app.example.test', 'token', 'verified')`,
		userID, projectID, deployID); err != nil {
		t.Fatalf("seed custom domain: %v", err)
	}
	return NewRepository(db, "apps.example.test"), deployID
}

func TestRepositoryResolvesGeneratedAndVerifiedCustomHosts(t *testing.T) {
	repo, deployID := newEdgeRepository(t)
	wantTarget := "http://snaphost-deploy-" + deployID + ":4173"

	for _, host := range []string{
		"proj-live.apps.example.test",
		"Proj-Live.Apps.Example.Test.:443",
		"app.example.test",
	} {
		route, err := repo.Resolve(context.Background(), host)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", host, err)
		}
		if route.DeployID != deployID || route.Target != wantTarget {
			t.Errorf("Resolve(%q) = %#v, want deploy %s target %s", host, route, deployID, wantTarget)
		}
	}
}

func TestRepositoryFailsClosedForAnythingNotRoutable(t *testing.T) {
	repo, deployID := newEdgeRepository(t)

	for _, host := range []string{
		"unknown.apps.example.test",
		"nested.proj-live.apps.example.test",
		"unknown.example.test",
		"apps.example.test",
	} {
		if _, err := repo.Resolve(context.Background(), host); !errors.Is(err, ErrRouteNotFound) {
			t.Errorf("Resolve(%q) error = %v, want ErrRouteNotFound", host, err)
		}
	}

	if _, err := repo.db.Exec(`UPDATE deploys SET status = 'stopped' WHERE id = ?`, deployID); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"proj-live.apps.example.test", "app.example.test"} {
		if _, err := repo.Resolve(context.Background(), host); !errors.Is(err, ErrRouteNotFound) {
			t.Errorf("stopped Resolve(%q) error = %v, want ErrRouteNotFound", host, err)
		}
	}
}

func TestRepositoryRequiresVerifiedCustomDomain(t *testing.T) {
	repo, _ := newEdgeRepository(t)

	if _, err := repo.db.Exec(`UPDATE custom_domains SET status = 'pending' WHERE domain = 'app.example.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(context.Background(), "app.example.test"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("pending custom domain error = %v, want ErrRouteNotFound", err)
	}

	// Changing custom-domain state must not disturb the generated route to the
	// same running deploy.
	if _, err := repo.Resolve(context.Background(), "proj-live.apps.example.test"); err != nil {
		t.Fatalf("generated route after custom-domain change: %v", err)
	}
}
