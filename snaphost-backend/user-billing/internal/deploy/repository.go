// Package deploy provides data access for the deploys table.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deploy source types. The source abstraction (Task 14b) decides how the
// builder obtains the project: cloning a public repo, cloning a private
// repo with a short-lived credential (14b-3), or unpacking an uploaded
// archive blob (14b-2).
const (
	SourceGitPublic  = "git_public"
	SourceGitPrivate = "git_private"
	SourceArchive    = "archive"
)

// ValidSourceType reports whether s is a known deploy source type.
func ValidSourceType(s string) bool {
	switch s {
	case SourceGitPublic, SourceGitPrivate, SourceArchive:
		return true
	}
	return false
}

// Deploy represents a user deployment record. A deploy is immutable once
// built: it is one build result, permanently addressable at its own
// subdomain. The mutable publish target above it is the project (Task 16a),
// and the pointer between them is an alias in custom_domains.
type Deploy struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	ProjectID       *uuid.UUID `json:"project_id,omitempty"`
	SourceType      string     `json:"source_type"`
	RepoURL         string     `json:"repo_url"`
	Branch          string     `json:"branch"`
	UploadID        *string    `json:"upload_id,omitempty"`
	CommitSHA       *string    `json:"commit_sha,omitempty"`
	Status          string     `json:"status"`
	ImageRef        *string    `json:"image_ref,omitempty"`
	EndpointURL     *string    `json:"endpoint_url,omitempty"`
	Subdomain       *string    `json:"subdomain,omitempty"`
	ContainerID     *string    `json:"container_id,omitempty"`
	CostVibecoins   int64      `json:"cost_vibecoins"`
	ReservationTxID *uuid.UUID `json:"reservation_tx_id,omitempty"`
	TTLExpiresAt    *time.Time `json:"ttl_expires_at,omitempty"`
	FailureReason   *string    `json:"failure_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StoppedAt       *time.Time `json:"stopped_at,omitempty"`
}

// ErrRouteNotFound indicates no running deploy is routable for the requested host.
var ErrRouteNotFound = errors.New("route not found")

// GCPolicy configures what the reclaim sweep is allowed to take away from a
// project beyond plain TTL expiry (Task 16a items 7 and 8). Both limits are
// disabled when zero.
type GCPolicy struct {
	// AliasIdleDays is how long an alias-pinned deploy may go without a
	// single request before the alias is unpinned and the runtime released.
	// A hard timer cannot express "live but quiet"; traffic can.
	AliasIdleDays int
	// KeepPerProject is how many superseded deploys of a project stay
	// running as rollback targets. Older ones are reclaimed — registry
	// storage, not idle runtime, is the accumulating cost.
	KeepPerProject int
}

// Repository provides CRUD data access for the deploys table.
// Called by repo-handler and runner-svc, not by frontend directly.
type Repository struct {
	pool *pgxpool.Pool
	// domainSuffix is the platform's own runtime suffix. Hosts under it
	// resolve through deploys.subdomain; anything else is a custom domain
	// and resolves through the alias table.
	domainSuffix string
	gc           GCPolicy
}

// Option configures a Repository at construction.
type Option func(*Repository)

// WithDomainSuffix sets the platform runtime suffix used to split route
// lookups between generated subdomains and custom domains.
func WithDomainSuffix(suffix string) Option {
	return func(r *Repository) { r.domainSuffix = strings.TrimSpace(strings.ToLower(suffix)) }
}

// WithGCPolicy sets the reclaim limits applied on top of TTL expiry.
func WithGCPolicy(p GCPolicy) Option {
	return func(r *Repository) { r.gc = p }
}

// NewRepository creates a new deploy repository.
func NewRepository(pool *pgxpool.Pool, opts ...Option) *Repository {
	r := &Repository{pool: pool}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Create inserts a new deploy record. An empty RepoURL is stored as NULL
// (archive deploys have no repository).
func (r *Repository) Create(ctx context.Context, d Deploy) error {
	if d.SourceType == "" {
		d.SourceType = SourceGitPublic
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO deploys (id, user_id, project_id, source_type, repo_url, branch, upload_id, commit_sha, status, cost_vibecoins, reservation_tx_id, metadata)
		 VALUES ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10, $11, '{}'::jsonb)`,
		d.ID, d.UserID, d.ProjectID, d.SourceType, d.RepoURL, d.Branch, d.UploadID, d.CommitSHA, d.Status, d.CostVibecoins, d.ReservationTxID,
	)
	if err != nil {
		return fmt.Errorf("create deploy: %w", err)
	}
	return nil
}

// deployColumns is the shared SELECT list for full Deploy rows; scanDeploy
// consumes it in the same order.
const deployColumns = `id, user_id, project_id, source_type, coalesce(repo_url, ''), branch, upload_id, commit_sha, status,
	        image_ref, endpoint_url, subdomain, container_id, cost_vibecoins, reservation_tx_id, ttl_expires_at,
	        failure_reason, created_at, updated_at, stopped_at`

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDeploy(row rowScanner) (Deploy, error) {
	var d Deploy
	err := row.Scan(&d.ID, &d.UserID, &d.ProjectID, &d.SourceType, &d.RepoURL, &d.Branch, &d.UploadID, &d.CommitSHA, &d.Status,
		&d.ImageRef, &d.EndpointURL, &d.Subdomain, &d.ContainerID, &d.CostVibecoins,
		&d.ReservationTxID, &d.TTLExpiresAt, &d.FailureReason,
		&d.CreatedAt, &d.UpdatedAt, &d.StoppedAt)
	return d, err
}

// Get retrieves a single deploy by ID.
func (r *Repository) Get(ctx context.Context, deployID uuid.UUID) (*Deploy, error) {
	d, err := scanDeploy(r.pool.QueryRow(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE id = $1`,
		deployID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("deploy not found: %w", err)
		}
		return nil, fmt.Errorf("get deploy: %w", err)
	}
	return &d, nil
}

// ListByUser returns paginated deploys for a user, ordered by creation time descending.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Deploy, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys by user: %w", err)
	}
	return collectDeploys(rows)
}

// ListByProject returns a project's deploys newest first — the rollback
// candidates for an alias, each still addressable at its own subdomain.
func (r *Repository) ListByProject(ctx context.Context, projectID uuid.UUID, limit int) ([]Deploy, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+deployColumns+`
		 FROM deploys
		 WHERE project_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys by project: %w", err)
	}
	return collectDeploys(rows)
}

func collectDeploys(rows pgx.Rows) ([]Deploy, error) {
	defer rows.Close()

	var deploys []Deploy
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy row: %w", err)
		}
		deploys = append(deploys, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deploy rows: %w", err)
	}

	return deploys, nil
}

// UpdateStatus transitions a deploy to a new status with an optional failure reason.
func (r *Repository) UpdateStatus(ctx context.Context, deployID uuid.UUID, status string, failureReason *string) error {
	tag, err := r.pool.Exec(ctx,
		updateStatusSQL,
		status, failureReason, deployID,
	)
	if err != nil {
		return fmt.Errorf("update deploy status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

const updateStatusSQL = `UPDATE deploys
 SET status = $1,
     failure_reason = $2,
     stopped_at = CASE
         WHEN $1 = 'stopped' THEN COALESCE(stopped_at, now())
         ELSE stopped_at
     END,
     updated_at = now()
 WHERE id = $3`

// SetRunning transitions a deploy to running status with its runtime details.
func (r *Repository) SetRunning(ctx context.Context, deployID uuid.UUID, imageRef, endpointURL, subdomain, containerID string, ttlExpiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deploys
		 SET status = 'running', image_ref = $1, endpoint_url = $2, subdomain = $3,
		     container_id = $4, ttl_expires_at = $5, updated_at = now()
		 WHERE id = $6`,
		imageRef, endpointURL, subdomain, containerID, ttlExpiresAt, deployID,
	)
	if err != nil {
		return fmt.Errorf("set deploy running: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

// MarkDeleted transitions a deploy to deleted and records when it stopped.
func (r *Repository) MarkDeleted(ctx context.Context, deployID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deploys
		 SET status = 'deleted', stopped_at = now(), updated_at = now()
		 WHERE id = $1`,
		deployID,
	)
	if err != nil {
		return fmt.Errorf("mark deploy deleted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deploy not found: %s", deployID)
	}
	return nil
}

// aliasPinned is true for a deploy an alias currently publishes. Such a
// deploy is the live site on someone's own hostname, so plain TTL expiry must
// never take it away — only the idle sweep below can, and only after it has
// been unpinned first.
const aliasPinned = `EXISTS (
	    SELECT 1 FROM custom_domains cd
	    WHERE cd.target_deploy_id = deploys.id AND cd.status = 'verified')`

// FindExpired returns IDs of running, un-aliased deploys whose TTL has expired.
func (r *Repository) FindExpired(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM deploys
		 WHERE status = 'running' AND ttl_expires_at < now() AND NOT `+aliasPinned+`
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("find expired deploys: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired deploy id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired deploy ids: %w", err)
	}

	return ids, nil
}

// ExpiredDeploy holds the minimal fields needed by runner-svc to stop an expired container.
type ExpiredDeploy struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	ContainerID *string   `json:"container_id"`
}

// reclaimableSQL selects everything the watchdog may stop. Three sources
// converge on one list, so the watchdog itself needs no new endpoint:
//
//  1. TTL expiry of un-aliased deploys — today's behavior, unchanged.
//  2. Deploys an alias pinned until the idle sweep unpinned them (item 7).
//     They are ordinary un-aliased deploys by the time this query runs.
//  3. Superseded deploys beyond the project's rollback retention (item 8).
//
// A deploy an alias still points at appears in none of them.
const reclaimableSQL = `
WITH ranked AS (
    SELECT id,
           row_number() OVER (PARTITION BY project_id ORDER BY created_at DESC) AS rank
    FROM deploys
    WHERE status = 'running' AND project_id IS NOT NULL
)
SELECT id, user_id, container_id
FROM deploys
WHERE status = 'running'
  AND NOT ` + aliasPinned + `
  AND (
        ttl_expires_at < now()
        OR ($2 > 0 AND id IN (SELECT id FROM ranked WHERE rank > $2))
      )
ORDER BY ttl_expires_at NULLS LAST
LIMIT $1`

// unpinIdleAliasesSQL releases an alias whose target has served no request
// for the configured window. The domain stays verified — the user still owns
// it — but stops resolving until the next deploy repoints it, exactly as a
// dead subdomain does. Detaching the pointer is what returns the deploy to
// ordinary GC.
const unpinIdleAliasesSQL = `
UPDATE custom_domains cd
SET target_deploy_id = NULL,
    last_error = 'unpinned_idle',
    updated_at = now()
FROM deploys d
WHERE cd.target_deploy_id = d.id
  AND cd.status = 'verified'
  AND coalesce(d.last_request_at, d.updated_at) < now() - make_interval(days => $1::int)`

// FindExpiredWithDetails returns the deploys the watchdog should stop: TTL
// expiry plus the alias-aware reclaim cases. Idle aliases are unpinned first,
// in the same sweep, so a released deploy is picked up on this pass.
func (r *Repository) FindExpiredWithDetails(ctx context.Context, limit int) ([]ExpiredDeploy, error) {
	if r.gc.AliasIdleDays > 0 {
		if _, err := r.pool.Exec(ctx, unpinIdleAliasesSQL, r.gc.AliasIdleDays); err != nil {
			return nil, fmt.Errorf("unpin idle aliases: %w", err)
		}
	}

	rows, err := r.pool.Query(ctx, reclaimableSQL, limit, r.gc.KeepPerProject)
	if err != nil {
		return nil, fmt.Errorf("find expired deploys with details: %w", err)
	}
	defer rows.Close()

	var deploys []ExpiredDeploy
	for rows.Next() {
		var d ExpiredDeploy
		if err := rows.Scan(&d.ID, &d.UserID, &d.ContainerID); err != nil {
			return nil, fmt.Errorf("scan expired deploy: %w", err)
		}
		deploys = append(deploys, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired deploys: %w", err)
	}

	return deploys, nil
}

// RouteInfo is the minimal running deploy mapping returned to the central router.
type RouteInfo struct {
	DeployID    string `json:"deploy_id"`
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Host        string `json:"host"`
}

// subdomainRouteSQL resolves a generated *.${DOMAIN_SUFFIX} host through the
// deploy's own immutable subdomain — the pre-16 behavior, unchanged.
const subdomainRouteSQL = `
SELECT id, container_id, status
FROM deploys
WHERE subdomain = $1
  AND status = 'running'
  AND container_id IS NOT NULL
  AND container_id <> ''
LIMIT 1`

// customDomainRouteSQL resolves a user-owned hostname through the alias. Only
// a verified domain resolves: an unverified or revoked one is indistinguishable
// from an unknown host, which is what keeps a hostname someone else controls
// from being served.
const customDomainRouteSQL = `
SELECT d.id, d.container_id, d.status
FROM custom_domains cd
JOIN deploys d ON d.id = cd.target_deploy_id
WHERE cd.domain = $1
  AND cd.status = 'verified'
  AND d.status = 'running'
  AND d.container_id IS NOT NULL
  AND d.container_id <> ''
LIMIT 1`

// FindRouteByHost returns a running deploy route for the given HTTP Host.
// Hosts under the platform suffix keep resolving through deploys.subdomain;
// any other host is a custom domain and resolves through the alias table.
// Both branches end on the same condition — running, with a container.
func (r *Repository) FindRouteByHost(ctx context.Context, host string) (*RouteInfo, error) {
	normalizedHost, subdomain, err := normalizeRouteHost(host)
	if err != nil {
		return nil, err
	}

	query, arg := customDomainRouteSQL, normalizedHost
	if r.isPlatformHost(normalizedHost) {
		query, arg = subdomainRouteSQL, subdomain
	}

	var deployID uuid.UUID
	var containerID string
	var status string
	err = r.pool.QueryRow(ctx, query, arg).Scan(&deployID, &containerID, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRouteNotFound
		}
		return nil, fmt.Errorf("find route by host: %w", err)
	}

	r.touchLastRequest(ctx, deployID)

	return &RouteInfo{
		DeployID:    deployID.String(),
		ContainerID: containerID,
		Status:      status,
		Host:        normalizedHost,
	}, nil
}

func normalizeRouteHost(host string) (normalizedHost, subdomain string, err error) {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return "", "", fmt.Errorf("host is required")
	}
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", "", fmt.Errorf("host is required")
	}
	parts := strings.Split(host, ".")
	if parts[0] == "" {
		return "", "", fmt.Errorf("host %q has empty subdomain", host)
	}
	return host, parts[0], nil
}

// isPlatformHost reports whether the host lives under our own runtime suffix.
// With no suffix configured every host takes the legacy subdomain branch, so
// an unset DOMAIN_SUFFIX degrades to exactly the pre-16 behavior.
func (r *Repository) isPlatformHost(host string) bool {
	if r.domainSuffix == "" {
		return true
	}
	return strings.HasSuffix(host, "."+r.domainSuffix)
}

// touchLastRequest records that this deploy served traffic, at most once per
// touchInterval. Best-effort: route lookup is the request hot path, so a
// failed write must never fail the request. The throttle is what keeps this
// from being one UPDATE per HTTP request.
func (r *Repository) touchLastRequest(ctx context.Context, deployID uuid.UUID) {
	_, _ = r.pool.Exec(ctx,
		`UPDATE deploys
		 SET last_request_at = now()
		 WHERE id = $1
		   AND (last_request_at IS NULL OR last_request_at < now() - make_interval(mins => $2::int))`,
		deployID, touchIntervalMinutes,
	)
}

// touchIntervalMinutes bounds last_request_at write frequency per deploy. The
// idle window it feeds is measured in days, so minute-grained resolution is
// far more than the GC decision needs.
const touchIntervalMinutes = 5
