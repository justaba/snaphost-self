package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"

	"go.uber.org/zap"
)

// newMigrated opens a fresh database in a temp directory and applies the
// baseline to it.
func newMigrated(t *testing.T) *sql.DB {
	t.Helper()

	handle, err := Open(context.Background(), filepath.Join(t.TempDir(), "snaphost.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })

	if err := RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return handle
}

// TestBaselineApplies runs the embedded migrations against a real, empty
// SQLite database and checks the schema they produce.
//
// This test exists because of a specific failure. Migration 0013 of the
// inherited history dropped the billing tables without dropping the two views
// built on them, so it failed on every install — and nothing noticed until the
// stack was started by hand, because no test had ever applied a migration to a
// real database. A schema file that is never executed is a guess.
func TestBaselineApplies(t *testing.T) {
	handle := newMigrated(t)

	rows, err := handle.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	sort.Strings(got)

	// schema_migrations is golang-migrate's own bookkeeping table.
	want := []string{
		"admin_audit_log",
		"ai_dockerfile_cache", "ai_usage_log", "api_keys", "custom_domains",
		"deploy_sagas", "deploys", "projects", "schema_migrations", "sessions",
		"users",
	}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables = %v, want %v", got, want)
		}
	}
}

// TestBaselineDownLeavesNothing checks that the down migration is a real
// reversal rather than a file that exists to satisfy the tool.
func TestBaselineDownLeavesNothing(t *testing.T) {
	handle := newMigrated(t)

	if err := rollback(handle); err != nil {
		t.Fatalf("roll baseline back: %v", err)
	}

	var remaining int
	err := handle.QueryRow(
		`SELECT count(*) FROM sqlite_master
		 WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`,
	).Scan(&remaining)
	if err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if remaining != 0 {
		t.Errorf("after down migration %d tables remain, want 0", remaining)
	}
}

// TestForeignKeysAreEnforced guards the pragma rather than the schema. SQLite
// ignores foreign keys unless every connection turns them on, so a declaration
// without the pragma is a comment that looks like a constraint.
func TestForeignKeysAreEnforced(t *testing.T) {
	handle := newMigrated(t)

	_, err := handle.Exec(
		`INSERT INTO custom_domains (id, user_id, project_id, domain, verification_token)
		 VALUES ('d1', 'u1', 'project-that-does-not-exist', 'example.test', 'token')`)
	if err == nil {
		t.Fatal("inserted a domain against a missing project; foreign keys are not enforced")
	}
}

// TestUpdatedAtTriggerFires checks one of the five updated_at triggers. They
// replaced a single shared PL/pgSQL function with one body per table, which is
// five chances to typo a table name into a trigger that silently does nothing.
func TestUpdatedAtTriggerFires(t *testing.T) {
	handle := newMigrated(t)

	if _, err := handle.Exec(
		`INSERT INTO projects (id, user_id, slug, source_key, created_at, updated_at)
		 VALUES ('p1', 'u1', 'demo', 'git:example#main', '2020-01-01T00:00:00.000Z', '2020-01-01T00:00:00.000Z')`,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	if _, err := handle.Exec(`UPDATE projects SET slug = 'renamed' WHERE id = 'p1'`); err != nil {
		t.Fatalf("update project: %v", err)
	}

	var updatedAt string
	if err := handle.QueryRow(`SELECT updated_at FROM projects WHERE id = 'p1'`).Scan(&updatedAt); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if updatedAt == "2020-01-01T00:00:00.000Z" {
		t.Error("updated_at was not touched by the trigger")
	}
}

// TestTimestampDefaultIsSortableUTC pins the timestamp format. Ordering across
// the whole schema is string ordering, which is only correct because every
// timestamp is RFC 3339 in UTC — a local time written here would sort wrongly
// and nothing would report an error.
func TestTimestampDefaultIsSortableUTC(t *testing.T) {
	handle := newMigrated(t)

	if _, err := handle.Exec(
		`INSERT INTO users (id, email) VALUES ('u1', 'someone@example.test')`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var createdAt string
	if err := handle.QueryRow(`SELECT created_at FROM users WHERE id = 'u1'`).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}

	// 2026-08-29T09:14:44.123Z
	if len(createdAt) != 24 || createdAt[10] != 'T' || createdAt[23] != 'Z' {
		t.Errorf("created_at = %q, want RFC 3339 UTC with milliseconds", createdAt)
	}
}
