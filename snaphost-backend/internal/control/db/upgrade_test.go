package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	migrate "github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

// Upgrading an installed database is the case the baseline rule cannot cover,
// and it is not hypothetical: golang-migrate applies only migrations above the
// recorded version, so a schema change folded into 0001 never runs on a
// database that already has 0001. The process starts fine and the first
// request touching the changed table answers `no such column`.
//
// These tests exist because that is invisible to every other test in this
// package: they all build a database from nothing, where an edited baseline
// and a real migration are indistinguishable.

// migrateTo applies migrations up to and including one version, leaving the
// database exactly as an older install would have it.
func migrateTo(t *testing.T, handle *sql.DB, version uint) {
	t.Helper()

	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}
	dbDriver, err := migratesqlite.WithInstance(handle, &migratesqlite.Config{})
	if err != nil {
		t.Fatalf("create migration driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func openTemp(t *testing.T) *sql.DB {
	t.Helper()

	handle, err := Open(context.Background(), filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

func hasColumn(t *testing.T, handle *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	err := handle.QueryRow(
		`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	if err != nil {
		t.Fatalf("inspect %s.%s: %v", table, column, err)
	}
	return n > 0
}

func hasObject(t *testing.T, handle *sql.DB, kind, name string) bool {
	t.Helper()
	var n int
	err := handle.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&n)
	if err != nil {
		t.Fatalf("inspect %s %s: %v", kind, name, err)
	}
	return n > 0
}

// The whole point: a database that stopped at the baseline must gain
// everything 0002 adds when the new binary starts.
func TestUpgradeFromBaselineAddsImageGCAndAudit(t *testing.T) {
	handle := openTemp(t)
	migrateTo(t, handle, 1)

	if hasColumn(t, handle, "deploys", "image_deleted_at") {
		t.Fatal("the baseline already has image_deleted_at; this test would prove nothing")
	}
	if hasObject(t, handle, "table", "admin_audit_log") {
		t.Fatal("the baseline already has admin_audit_log; this test would prove nothing")
	}

	if err := RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("upgrade from version 1: %v", err)
	}

	if !hasColumn(t, handle, "deploys", "image_deleted_at") {
		t.Error("deploys.image_deleted_at is missing after the upgrade")
	}
	for _, index := range []string{"idx_deploys_image_cleanup", "idx_deploys_stopped_at"} {
		if !hasObject(t, handle, "index", index) {
			t.Errorf("%s is missing after the upgrade", index)
		}
	}
	if !hasObject(t, handle, "table", "admin_audit_log") {
		t.Error("admin_audit_log is missing after the upgrade")
	}
}

// A schema that exists is not the same as one the queries can use. These are
// the statements that would answer `no such column` on an un-upgraded install.
func TestUpgradedSchemaAnswersTheNewQueries(t *testing.T) {
	handle := openTemp(t)
	migrateTo(t, handle, 1)
	if err := RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	var n int
	if err := handle.QueryRow(`
		SELECT count(*) FROM deploys
		WHERE status IN ('failed', 'deleted')
		  AND image_ref IS NOT NULL AND image_deleted_at IS NULL`).Scan(&n); err != nil {
		t.Errorf("the image cleanup query does not run after an upgrade: %v", err)
	}
	if err := handle.QueryRow(`SELECT count(*) FROM admin_audit_log`).Scan(&n); err != nil {
		t.Errorf("the audit query does not run after an upgrade: %v", err)
	}
	if _, err := handle.Exec(
		`UPDATE deploys SET image_deleted_at = ? WHERE id = ?`, Now(), "missing"); err != nil {
		t.Errorf("the cleanup marker cannot be written after an upgrade: %v", err)
	}
}

// Existing rows must survive, with the new column null rather than absent —
// that null is what puts an already-built deploy into the cleanup queue.
func TestUpgradePreservesRowsAndQueuesTheirImages(t *testing.T) {
	handle := openTemp(t)
	migrateTo(t, handle, 1)

	if _, err := handle.Exec(`INSERT INTO users (id, email) VALUES ('u1', 'op@example.test')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := handle.Exec(`
		INSERT INTO deploys (id, user_id, source_type, status, image_ref)
		VALUES ('d1', 'u1', 'git_public', 'failed', 'snaphost/proj-abc:d1')`); err != nil {
		t.Fatalf("seed deploy: %v", err)
	}

	if err := RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	var imageRef string
	var deletedAt *string
	if err := handle.QueryRow(
		`SELECT image_ref, image_deleted_at FROM deploys WHERE id = 'd1'`).Scan(&imageRef, &deletedAt); err != nil {
		t.Fatalf("read the pre-existing deploy: %v", err)
	}
	if imageRef != "snaphost/proj-abc:d1" {
		t.Errorf("image_ref = %q; the upgrade lost data", imageRef)
	}
	if deletedAt != nil {
		t.Errorf("image_deleted_at = %q, want null so the sweep collects it", *deletedAt)
	}
}

// Down from 0002 has to be a real reversal, or the next `migrate down` leaves
// a column the baseline does not declare and the schema stops matching itself.
func TestSecondMigrationIsReversible(t *testing.T) {
	handle := openTemp(t)
	if err := RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	migrateTo(t, handle, 1)

	if hasColumn(t, handle, "deploys", "image_deleted_at") {
		t.Error("image_deleted_at survived the down migration")
	}
	if hasObject(t, handle, "table", "admin_audit_log") {
		t.Error("admin_audit_log survived the down migration")
	}
	if hasObject(t, handle, "index", "idx_deploys_image_cleanup") {
		t.Error("idx_deploys_image_cleanup survived the down migration")
	}
}
