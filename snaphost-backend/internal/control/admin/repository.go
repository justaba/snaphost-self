package admin

import (
	"context"
	"errors"
	"fmt"

	"database/sql"
	"time"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Repository reads the operator view of the control plane's tables.
//
// The aggregate subqueries below scan whole tables rather than using
// incrementally maintained counters. That is a deliberate trade at this size:
// the numbers are always correct, and there is no second source of truth to
// drift. Revisit when a user list query stops being instant, not before.
type Repository struct {
	db *sql.DB
}

// NewRepository creates an admin repository over the given pool.
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

// Overview returns the platform-wide counters for the admin dashboard.
func (r *Repository) Overview(ctx context.Context) (*Overview, error) {
	var o Overview
	err := r.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM users),
		  (SELECT count(*) FROM deploys),
		  (SELECT count(*) FROM deploys WHERE status = 'running'),
		  (SELECT count(*) FROM deploys WHERE status = 'failed'),
		  (SELECT count(*) FROM deploys WHERE created_at > ?),
		  (SELECT count(*) FROM projects),
		  (SELECT count(*) FROM custom_domains WHERE status = 'pending'),
		  (SELECT count(*) FROM custom_domains WHERE status = 'verified'),
		  (SELECT count(*) FROM api_keys WHERE revoked_at IS NULL)
	`, controldb.FormatTime(time.Now().Add(-24*time.Hour))).Scan(
		&o.Users,
		&o.Deploys, &o.DeploysRunning, &o.DeploysFailed, &o.Deploys24h,
		&o.Projects, &o.DomainsPending, &o.DomainsActive, &o.ActiveAPIKeys,
	)
	if err != nil {
		return nil, fmt.Errorf("admin overview: %w", err)
	}
	return &o, nil
}

// userAggregates joins each account to the rollups the list shows. They are
// grouped once and joined, rather than run as correlated subqueries per row.
const userAggregates = `
FROM users u
LEFT JOIN (
    SELECT user_id,
           count(*)                                        AS total,
           count(*) FILTER (WHERE status = 'running')      AS running,
           count(*) FILTER (WHERE status = 'failed')       AS failed,
           max(created_at)                                 AS last_deploy_at
    FROM deploys GROUP BY user_id
) d ON d.user_id = u.id
LEFT JOIN (
    SELECT user_id, count(*) AS domains
    FROM custom_domains WHERE status <> 'revoked' GROUP BY user_id
) dom ON dom.user_id = u.id`

const userColumns = `
    u.id, u.email,
    coalesce(d.total, 0), coalesce(d.running, 0), coalesce(d.failed, 0),
    coalesce(dom.domains, 0),
    d.last_deploy_at, u.created_at`

// userSearch matches an email substring or an exact user id. An operator
// arriving from a log line has a UUID; one arriving from a support message has
// an email, so both have to work in the same box.
const userSearch = `(? = '' OR u.email LIKE '%' || ? || '%' OR u.id = ?)`

func scanUser(row rowScanner) (UserSummary, error) {
	var u UserSummary
	err := row.Scan(&u.ID, &u.Email,
		&u.DeploysTotal, &u.DeploysRunning, &u.DeploysFailed,
		&u.DomainsCount,
		controldb.IntoNull(&u.LastDeployAt), controldb.Into(&u.CreatedAt))
	return u, err
}

// rowScanner is satisfied by both sql.Row and sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// ListUsers returns one page of accounts, newest first, with their totals.
func (r *Repository) ListUsers(ctx context.Context, f Filter) (*Page[UserSummary], error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users u WHERE `+userSearch, userSearchArgs(f.Query)...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT`+userColumns+userAggregates+`
		 WHERE `+userSearch+`
		 ORDER BY u.created_at DESC
		 LIMIT ? OFFSET ?`,
		withPage(userSearchArgs(f.Query), f.Limit, f.Offset)...,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	items := []UserSummary{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user rows: %w", err)
	}

	return &Page[UserSummary]{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// GetUser returns one account with its projects, domains, and API keys.
func (r *Repository) GetUser(ctx context.Context, userID uuid.UUID) (*UserDetail, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx,
		`SELECT`+userColumns+userAggregates+` WHERE u.id = ?`, userID.String(),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	detail := &UserDetail{UserSummary: u}

	if detail.Projects, err = r.ListProjects(ctx, userID); err != nil {
		return nil, err
	}
	domains, err := r.ListDomains(ctx, Filter{UserID: &userID, Limit: 100})
	if err != nil {
		return nil, err
	}
	detail.Domains = domains.Items
	if detail.APIKeys, err = r.ListAPIKeys(ctx, userID); err != nil {
		return nil, err
	}

	return detail, nil
}

const deployColumns = `
    d.id, d.user_id, u.email, d.project_id, p.slug, d.source_type,
    coalesce(d.repo_url, ''), d.branch, d.commit_sha, d.status,
    d.image_ref, d.endpoint_url, d.subdomain, d.container_id,
    d.failure_reason, d.ttl_expires_at, d.last_request_at,
    d.created_at, d.updated_at, d.stopped_at`

const deployJoins = `
FROM deploys d
LEFT JOIN users u ON u.id = d.user_id
LEFT JOIN projects p ON p.id = d.project_id`

// deployFilter is shared by the list and its count. Its bindings are built by
// deployFilterArgs, which is where the repetition is documented — each mention
// of a value needs its own placeholder here.
const deployFilter = `
WHERE (? = '' OR d.status = ?)
  AND (? IS NULL OR d.user_id = ?)
  AND (? = '' OR d.id = ? OR d.repo_url LIKE '%' || ? || '%'
       OR d.subdomain LIKE '%' || ? || '%' OR u.email LIKE '%' || ? || '%')`

func scanDeploy(row rowScanner) (DeployRow, error) {
	var d DeployRow
	err := row.Scan(&d.ID, &d.UserID, &d.UserEmail, &d.ProjectID, &d.ProjectSlug, &d.SourceType,
		&d.RepoURL, &d.Branch, &d.CommitSHA, &d.Status,
		&d.ImageRef, &d.EndpointURL, &d.Subdomain, &d.ContainerID,
		&d.FailureReason, controldb.IntoNull(&d.TTLExpiresAt), controldb.IntoNull(&d.LastRequestAt),
		controldb.Into(&d.CreatedAt), controldb.Into(&d.UpdatedAt), controldb.IntoNull(&d.StoppedAt))
	return d, err
}

// ListDeploys returns one page of deploys across all accounts, newest first.
func (r *Repository) ListDeploys(ctx context.Context, f Filter) (*Page[DeployRow], error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*)`+deployJoins+deployFilter, deployFilterArgs(f.Status, f.UserID, f.Query)...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count deploys: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT`+deployColumns+deployJoins+deployFilter+`
		 ORDER BY d.created_at DESC
		 LIMIT ? OFFSET ?`,
		withPage(deployFilterArgs(f.Status, f.UserID, f.Query), f.Limit, f.Offset)...,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}
	defer rows.Close()

	items := []DeployRow{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy row: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deploy rows: %w", err)
	}

	return &Page[DeployRow]{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// GetDeploy returns one deploy with its saga, its published domains, and the
// ledger entries it produced.
func (r *Repository) GetDeploy(ctx context.Context, deployID uuid.UUID) (*DeployDetail, error) {
	d, err := scanDeploy(r.db.QueryRowContext(ctx,
		`SELECT`+deployColumns+deployJoins+` WHERE d.id = ?`, deployID.String(),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get deploy: %w", err)
	}

	detail := &DeployDetail{DeployRow: d, Domains: []DomainRow{}}

	saga, err := r.getSaga(ctx, deployID)
	if err != nil {
		return nil, err
	}
	detail.Saga = saga

	domains, err := r.listDomainsForDeploy(ctx, deployID)
	if err != nil {
		return nil, err
	}
	detail.Domains = domains

	return detail, nil
}

// getSaga returns the saga row for a deploy, or nil when none exists. A deploy
// created before the saga path, or one that never reached the worker, has no
// saga — that absence is information, not an error.
func (r *Repository) getSaga(ctx context.Context, deployID uuid.UUID) (*SagaRow, error) {
	var s SagaRow
	err := r.db.QueryRowContext(ctx, `
		SELECT deploy_id, current_step, image_built, container_running,
		       retry_count, failure_reason, last_error,
		       created_at, updated_at, started_at, completed_at
		FROM deploy_sagas WHERE deploy_id = ?`, deployID.String(),
	).Scan(&s.DeployID, &s.CurrentStep, &s.ImageBuilt, &s.ContainerRunning,
		&s.RetryCount, &s.FailureReason, &s.LastError,
		controldb.Into(&s.CreatedAt), controldb.Into(&s.UpdatedAt),
		controldb.IntoNull(&s.StartedAt), controldb.IntoNull(&s.CompletedAt))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get saga: %w", err)
	}
	return &s, nil
}

const domainColumns = `
    cd.id, cd.user_id, u.email, cd.project_id, cd.target_deploy_id, cd.domain,
    cd.status, cd.last_error, cd.verified_at, cd.last_checked_at, cd.created_at`

const domainJoins = `
FROM custom_domains cd
LEFT JOIN users u ON u.id = cd.user_id`

func scanDomain(row rowScanner) (DomainRow, error) {
	var d DomainRow
	err := row.Scan(&d.ID, &d.UserID, &d.UserEmail, &d.ProjectID, &d.TargetDeployID, &d.Domain,
		&d.Status, &d.LastError, controldb.IntoNull(&d.VerifiedAt),
		controldb.IntoNull(&d.LastCheckedAt), controldb.Into(&d.CreatedAt))
	return d, err
}

// ListDomains returns one page of custom domains, newest first.
func (r *Repository) ListDomains(ctx context.Context, f Filter) (*Page[DomainRow], error) {
	const filter = `
WHERE (? = '' OR cd.status = ?)
  AND (? IS NULL OR cd.user_id = ?)
  AND (? = '' OR cd.domain LIKE '%' || ? || '%' OR u.email LIKE '%' || ? || '%')`

	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*)`+domainJoins+filter, domainFilterArgs(f.Status, f.UserID, f.Query)...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count domains: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT`+domainColumns+domainJoins+filter+`
		 ORDER BY cd.created_at DESC
		 LIMIT ? OFFSET ?`,
		withPage(domainFilterArgs(f.Status, f.UserID, f.Query), f.Limit, f.Offset)...,
	)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	items := []DomainRow{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan domain row: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domain rows: %w", err)
	}

	return &Page[DomainRow]{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

func (r *Repository) listDomainsForDeploy(ctx context.Context, deployID uuid.UUID) ([]DomainRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT`+domainColumns+domainJoins+`
		 WHERE cd.target_deploy_id = ? ORDER BY cd.domain`, deployID.String())
	if err != nil {
		return nil, fmt.Errorf("list deploy domains: %w", err)
	}
	defer rows.Close()

	items := []DomainRow{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy domain row: %w", err)
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

// ListProjects returns an account's publish targets with their build counts.
func (r *Repository) ListProjects(ctx context.Context, userID uuid.UUID) ([]ProjectRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.user_id, p.slug, p.source_key,
		       (SELECT count(*) FROM deploys d WHERE d.project_id = p.id),
		       p.created_at
		FROM projects p
		WHERE p.user_id = ?
		ORDER BY p.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	items := []ProjectRow{}
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey, &p.DeploysCount, controldb.Into(&p.CreatedAt)); err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// ListAPIKeys returns an account's keys. The hash column is never selected:
// it is the only stored credential in this database and no operator screen
// has a use for it.
func (r *Repository) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKeyRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, key_prefix, name, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	items := []APIKeyRow{}
	for rows.Next() {
		var k APIKeyRow
		if err := rows.Scan(&k.ID, &k.UserID, &k.Prefix, &k.Name, controldb.Into(&k.CreatedAt), controldb.IntoNull(&k.LastUsedAt), controldb.IntoNull(&k.RevokedAt)); err != nil {
			return nil, fmt.Errorf("scan api key row: %w", err)
		}
		items = append(items, k)
	}
	return items, rows.Err()
}
