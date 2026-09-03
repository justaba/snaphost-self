package deploy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// The reclaim sweep had never run. FindExpiredWithDetails passed two arguments
// to a statement with four placeholders, in the wrong order, and skipped the
// timestamp entirely; SQLite answered "missing argument with index 3" every
// tick of the watchdog, so no deploy was ever stopped for TTL expiry.
//
// It was not caught because every test in this package inspected the SQL as a
// string. An arity mismatch is invisible to that: the text is fine, the call
// is not. These tests execute the statements against a real database with the
// baseline applied, which is the only way the defect shows.
func newReclaimDB(t *testing.T) *Repository {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "reclaim.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	return NewRepository(handle, WithGCPolicy(GCPolicy{KeepPerProject: 3}))
}

func TestReclaimQueriesExecute(t *testing.T) {
	repo := newReclaimDB(t)

	if _, err := repo.FindExpiredWithDetails(context.Background(), 10); err != nil {
		t.Fatalf("FindExpiredWithDetails: %v", err)
	}
}

// A policy of zero must not silently reclaim every deploy of a project: the
// retention branch is gated on the count being positive, and a wrong argument
// order would put the row limit into that gate.
func TestReclaimWithAnEmptyPolicyExecutes(t *testing.T) {
	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "reclaim.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	repo := NewRepository(handle)
	if _, err := repo.FindExpiredWithDetails(context.Background(), 10); err != nil {
		t.Fatalf("FindExpiredWithDetails: %v", err)
	}
}

// TTL expiry is compared against a timestamp this process formats. A row whose
// ttl_expires_at is in the past must come back; one in the future must not.
// Written with the two rows differing only in that column, because the failure
// this guards against — SQLite's own date functions, which write a space where
// the schema writes a T — matches everything rather than nothing.
func TestReclaimSelectsOnlyExpiredDeploys(t *testing.T) {
	repo := newReclaimDB(t)
	ctx := context.Background()

	owner := uuid.New()
	past := seedRunningDeploy(t, repo, owner, time.Now().Add(-time.Hour))
	future := seedRunningDeploy(t, repo, owner, time.Now().Add(time.Hour))

	got, err := repo.FindExpiredWithDetails(ctx, 10)
	if err != nil {
		t.Fatalf("FindExpiredWithDetails: %v", err)
	}

	var ids []uuid.UUID
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if len(ids) != 1 || ids[0] != past {
		t.Fatalf("reclaimed %v, want exactly the expired deploy %v (the live one is %v)", ids, past, future)
	}
}

func seedRunningDeploy(t *testing.T, repo *Repository, owner uuid.UUID, ttl time.Time) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := repo.db.ExecContext(context.Background(),
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status, ttl_expires_at)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', 'running', ?)`,
		id, owner, controldb.FormatTime(ttl),
	)
	if err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	return id
}
