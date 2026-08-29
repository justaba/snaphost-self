package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestScanRoundTrip pins what the driver does and does not convert, because
// the repositories are written against this answer: ids scan straight into
// uuid.UUID because google/uuid implements sql.Scanner, and timestamps do not
// scan into time.Time at all, so they go through Into and IntoNull.
func TestScanRoundTrip(t *testing.T) {
	handle := newMigrated(t)

	id := uuid.New()
	verified := time.Date(2026, 3, 4, 5, 6, 7, 800*int(time.Millisecond), time.UTC)

	if _, err := handle.Exec(
		`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, 'demo', 'git:x#main')`,
		id.String(), uuid.New().String(),
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := handle.Exec(
		`INSERT INTO custom_domains (id, user_id, project_id, domain, verification_token, verified_at)
		 VALUES (?, ?, ?, 'example.test', 'token', ?)`,
		uuid.NewString(), uuid.NewString(), id.String(), FormatTime(verified),
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	var (
		gotID        uuid.UUID
		gotCreated   time.Time
		gotVerified  *time.Time
		gotUnchecked *time.Time
	)
	err := handle.QueryRow(
		`SELECT p.id, p.created_at, d.verified_at, d.last_checked_at
		 FROM projects p JOIN custom_domains d ON d.project_id = p.id
		 WHERE p.id = ?`, id.String(),
	).Scan(&gotID, Into(&gotCreated), IntoNull(&gotVerified), IntoNull(&gotUnchecked))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if gotID != id {
		t.Errorf("id = %s, want %s", gotID, id)
	}
	if gotCreated.IsZero() {
		t.Error("created_at scanned as zero")
	}
	if gotVerified == nil || !gotVerified.Equal(verified) {
		t.Errorf("verified_at = %v, want %s", gotVerified, verified)
	}
	if gotUnchecked != nil {
		t.Errorf("last_checked_at = %v, want nil for NULL", gotUnchecked)
	}
}
