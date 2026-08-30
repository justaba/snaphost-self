package deploy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

func newRunningStateDB(t *testing.T) *Repository {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "running-state.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return NewRepository(handle)
}

func seedFinalizableDeploy(t *testing.T, repo *Repository, withSaga bool) (uuid.UUID, uuid.UUID) {
	t.Helper()

	deployID := uuid.New()
	userID := uuid.New()
	if _, err := repo.db.ExecContext(context.Background(),
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', 'provisioning')`,
		deployID.String(), userID.String(),
	); err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	if withSaga {
		if _, err := repo.db.ExecContext(context.Background(),
			`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, image_built)
			 VALUES (?, ?, 'built', 1)`,
			deployID.String(), userID.String(),
		); err != nil {
			t.Fatalf("seed saga: %v", err)
		}
	}
	return deployID, userID
}

func TestSetRunningCommitsDeployAndSagaTogether(t *testing.T) {
	repo := newRunningStateDB(t)
	deployID, _ := seedFinalizableDeploy(t, repo, true)
	ttl := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)

	if err := repo.SetRunning(
		context.Background(), deployID,
		"registry.invalid/app:image", "https://deploy.example.test", "deploy", "container-id", ttl,
	); err != nil {
		t.Fatalf("SetRunning: %v", err)
	}

	got, err := repo.Get(context.Background(), deployID)
	if err != nil {
		t.Fatalf("Get deploy: %v", err)
	}
	if got.Status != "running" || got.ContainerID == nil || *got.ContainerID != "container-id" {
		t.Fatalf("deploy state = status %q container %#v, want running/container-id", got.Status, got.ContainerID)
	}
	if got.EndpointURL == nil || *got.EndpointURL != "https://deploy.example.test" {
		t.Fatalf("deploy endpoint = %#v", got.EndpointURL)
	}
	if got.TTLExpiresAt == nil || !got.TTLExpiresAt.Equal(ttl) {
		t.Fatalf("deploy TTL = %v, want %v", got.TTLExpiresAt, ttl)
	}

	var (
		step             string
		containerRunning bool
		containerID      string
		endpointURL      string
		startedAt        string
	)
	if err := repo.db.QueryRowContext(context.Background(),
		`SELECT current_step, container_running, container_id, endpoint_url, started_at
		 FROM deploy_sagas WHERE deploy_id = ?`, deployID.String(),
	).Scan(&step, &containerRunning, &containerID, &endpointURL, &startedAt); err != nil {
		t.Fatalf("read saga state: %v", err)
	}
	if step != "provisioning" || !containerRunning || containerID != "container-id" || endpointURL != "https://deploy.example.test" || startedAt == "" {
		t.Fatalf("saga state = (%q, %t, %q, %q, %q)", step, containerRunning, containerID, endpointURL, startedAt)
	}
}

func TestSetRunningRollsBackDeployWhenSagaCannotAdvance(t *testing.T) {
	repo := newRunningStateDB(t)
	deployID, _ := seedFinalizableDeploy(t, repo, false)

	err := repo.SetRunning(
		context.Background(), deployID,
		"registry.invalid/app:image", "https://deploy.example.test", "deploy", "container-id", time.Now().Add(time.Hour),
	)
	if err == nil || !strings.Contains(err.Error(), "runnable saga not found") {
		t.Fatalf("SetRunning() error = %v, want missing runnable saga", err)
	}

	got, getErr := repo.Get(context.Background(), deployID)
	if getErr != nil {
		t.Fatalf("Get deploy: %v", getErr)
	}
	if got.Status != "provisioning" || got.ContainerID != nil || got.EndpointURL != nil || got.ImageRef != nil {
		t.Fatalf("deploy update was not rolled back: %#v", got)
	}
}

func TestUpdateStatusSQLStoppedAtAccounting(t *testing.T) {
	required := []struct {
		name string
		want string
	}{
		{
			// The status is bound twice because SQLite's placeholders are
			// positional, and the timestamp is a parameter because its date
			// functions produce a format these columns do not use.
			name: "stopped status sets stopped_at",
			want: "WHEN ? = 'stopped' THEN COALESCE(stopped_at, ?)",
		},
		{
			name: "non stopped statuses preserve stopped_at",
			want: "ELSE stopped_at",
		},
	}

	normalized := strings.Join(strings.Fields(updateStatusSQL), " ")
	for _, tc := range required {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(normalized, tc.want) {
				t.Fatalf("updateStatusSQL missing %q in %q", tc.want, normalized)
			}
		})
	}

	if strings.Contains(normalized, "'deleted'") {
		t.Fatalf("generic status update should not special-case deleted; MarkDeleted owns deleted stopped_at behavior: %q", normalized)
	}
}

func TestIsPlatformHost(t *testing.T) {
	r := &Repository{}
	WithDomainSuffix("Snaphost.PW")(r)

	cases := map[string]bool{
		"proj-abc.snaphost.pw": true,
		"shop.example.com":     false,
		"snaphost.pw":          false, // apex is not a deploy subdomain
		"snaphost.pw.evil.com": false, // suffix must be a real suffix, not a substring
	}
	for host, want := range cases {
		if got := r.isPlatformHost(host); got != want {
			t.Fatalf("isPlatformHost(%q) = %v, want %v", host, got, want)
		}
	}

	// With no suffix configured every host takes the legacy subdomain
	// branch, so an unset DOMAIN_SUFFIX is exactly the pre-16 behavior.
	if !(&Repository{}).isPlatformHost("shop.example.com") {
		t.Fatal("unset domain suffix must fall back to the subdomain branch")
	}
}

func TestCustomDomainRouteRequiresVerifiedAndRunning(t *testing.T) {
	normalized := strings.Join(strings.Fields(customDomainRouteSQL), " ")
	for _, want := range []string{
		"cd.status = 'verified'",
		"d.status = 'running'",
		"d.container_id <> ''",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("custom domain lookup missing %q: an unverified or dead alias must not route: %s", want, normalized)
		}
	}
}

func TestReclaimNeverTakesAnAliasedDeploy(t *testing.T) {
	normalized := strings.Join(strings.Fields(reclaimableSQL), " ")
	if !strings.Contains(normalized, "AND NOT EXISTS ( SELECT 1 FROM custom_domains cd") {
		t.Fatalf("reclaim sweep must exclude alias-pinned deploys: %s", normalized)
	}
	// The cutoff is a bound parameter rather than now(): SQLite's date
	// functions produce a format that does not compare correctly against the
	// RFC 3339 text these columns hold, so every timestamp comparison is
	// computed in Go.
	if !strings.Contains(normalized, "ttl_expires_at < ?") {
		t.Fatalf("reclaim sweep must still honor TTL expiry: %s", normalized)
	}
	if !strings.Contains(normalized, "rank > ?") {
		t.Fatalf("reclaim sweep must apply per-project retention: %s", normalized)
	}

	// No cd. prefix: the statement was written with PostgreSQL's UPDATE ... FROM
	// and a table alias, which SQLite does not accept here. It is a correlated
	// EXISTS now, and the alias only survives on the deploys side.
	unpin := strings.Join(strings.Fields(unpinIdleAliasesSQL), " ")
	if !strings.Contains(unpin, "status = 'verified'") || !strings.Contains(unpin, "target_deploy_id = NULL") {
		t.Fatalf("idle sweep must unpin the alias before its target can be reclaimed: %s", unpin)
	}
}

func TestNormalizeRouteHost(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		wantHost      string
		wantSubdomain string
	}{
		{
			name:          "lowercase and port",
			in:            "Proj-ABC.snaphost.pw:443",
			wantHost:      "proj-abc.snaphost.pw",
			wantSubdomain: "proj-abc",
		},
		{
			name:          "trim trailing dot",
			in:            "proj-abc.snaphost.pw.",
			wantHost:      "proj-abc.snaphost.pw",
			wantSubdomain: "proj-abc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHost, gotSubdomain, err := normalizeRouteHost(tc.in)
			if err != nil {
				t.Fatalf("normalizeRouteHost() error = %v", err)
			}
			if gotHost != tc.wantHost || gotSubdomain != tc.wantSubdomain {
				t.Fatalf("normalizeRouteHost() = (%q, %q), want (%q, %q)", gotHost, gotSubdomain, tc.wantHost, tc.wantSubdomain)
			}
		})
	}
}
