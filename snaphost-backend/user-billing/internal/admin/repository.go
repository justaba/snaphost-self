package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool *pgxpool.Pool
}

// NewRepository creates an admin repository over the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Overview returns the platform-wide counters for the admin dashboard.
func (r *Repository) Overview(ctx context.Context) (*Overview, error) {
	var o Overview
	err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM users),
		  (SELECT count(*) FROM wallets),
		  (SELECT coalesce(sum(balance), 0) FROM wallets),
		  (SELECT coalesce(sum(reserved), 0) FROM wallets),
		  (SELECT count(*) FROM deploys),
		  (SELECT count(*) FROM deploys WHERE status = 'running'),
		  (SELECT count(*) FROM deploys WHERE status = 'failed'),
		  (SELECT count(*) FROM deploys WHERE created_at > now() - interval '24 hours'),
		  (SELECT count(*) FROM projects),
		  (SELECT count(*) FROM custom_domains WHERE status = 'pending'),
		  (SELECT count(*) FROM custom_domains WHERE status = 'verified'),
		  (SELECT count(*) FROM api_keys WHERE revoked_at IS NULL),
		  (SELECT coalesce(sum(amount), 0) FROM transactions WHERE type IN ('topup', 'bonus') AND status = 'completed'),
		  (SELECT coalesce(sum(amount), 0) FROM transactions WHERE type = 'commit' AND status = 'completed'),
		  (SELECT count(*) FROM users u WHERE NOT EXISTS (SELECT 1 FROM wallets w WHERE w.user_id = u.id))
	`).Scan(
		&o.Users, &o.Wallets, &o.TotalBalance, &o.TotalReserved,
		&o.Deploys, &o.DeploysRunning, &o.DeploysFailed, &o.Deploys24h,
		&o.Projects, &o.DomainsPending, &o.DomainsActive, &o.ActiveAPIKeys,
		&o.CoinsToppedUp, &o.CoinsSpent, &o.WalletlessUsers,
	)
	if err != nil {
		return nil, fmt.Errorf("admin overview: %w", err)
	}
	return &o, nil
}

// userAggregates joins each account to its wallet and to the rollups the list
// shows. Deploy and transaction rollups are grouped once and joined, rather
// than run as correlated subqueries per row.
const userAggregates = `
FROM users u
LEFT JOIN wallets w ON w.user_id = u.id
LEFT JOIN (
    SELECT user_id,
           count(*)                                        AS total,
           count(*) FILTER (WHERE status = 'running')      AS running,
           count(*) FILTER (WHERE status = 'failed')       AS failed,
           max(created_at)                                 AS last_deploy_at
    FROM deploys GROUP BY user_id
) d ON d.user_id = u.id
LEFT JOIN (
    SELECT user_id,
           coalesce(sum(amount) FILTER (WHERE type IN ('topup', 'bonus') AND status = 'completed'), 0) AS topped_up,
           coalesce(sum(amount) FILTER (WHERE type = 'commit' AND status = 'completed'), 0)            AS spent
    FROM transactions GROUP BY user_id
) t ON t.user_id = u.id
LEFT JOIN (
    SELECT user_id, count(*) AS domains
    FROM custom_domains WHERE status <> 'revoked' GROUP BY user_id
) dom ON dom.user_id = u.id`

const userColumns = `
    u.id, u.email, w.balance, w.reserved, (w.user_id IS NOT NULL) AS has_wallet,
    coalesce(d.total, 0), coalesce(d.running, 0), coalesce(d.failed, 0),
    coalesce(t.topped_up, 0), coalesce(t.spent, 0), coalesce(dom.domains, 0),
    d.last_deploy_at, u.created_at`

// userSearch matches an email substring or an exact user id. An operator
// arriving from a log line has a UUID; one arriving from a support message has
// an email, so both have to work in the same box.
const userSearch = `($1 = '' OR u.email ILIKE '%' || $1 || '%' OR u.id::text = $1)`

func scanUser(row rowScanner) (UserSummary, error) {
	var u UserSummary
	err := row.Scan(&u.ID, &u.Email, &u.Balance, &u.Reserved, &u.HasWallet,
		&u.DeploysTotal, &u.DeploysRunning, &u.DeploysFailed,
		&u.CoinsToppedUp, &u.CoinsSpent, &u.DomainsCount,
		&u.LastDeployAt, &u.CreatedAt)
	return u, err
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// ListUsers returns one page of accounts, newest first, with their totals.
func (r *Repository) ListUsers(ctx context.Context, f Filter) (*Page[UserSummary], error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM users u WHERE `+userSearch, f.Query,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT`+userColumns+userAggregates+`
		 WHERE `+userSearch+`
		 ORDER BY u.created_at DESC
		 LIMIT $2 OFFSET $3`,
		f.Query, f.Limit, f.Offset,
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
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT`+userColumns+userAggregates+` WHERE u.id = $1`, userID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
    d.image_ref, d.endpoint_url, d.subdomain, d.container_id, d.cost_vibecoins,
    d.failure_reason, d.ttl_expires_at, d.last_request_at,
    d.created_at, d.updated_at, d.stopped_at`

const deployJoins = `
FROM deploys d
LEFT JOIN users u ON u.id = d.user_id
LEFT JOIN projects p ON p.id = d.project_id`

// deployFilter is shared by the list and its count. $1 status, $2 user id,
// $3 free-text over id, repo url, subdomain, and image tag.
const deployFilter = `
WHERE ($1 = '' OR d.status = $1)
  AND ($2::uuid IS NULL OR d.user_id = $2)
  AND ($3 = '' OR d.id::text = $3 OR d.repo_url ILIKE '%' || $3 || '%'
       OR d.subdomain ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')`

func scanDeploy(row rowScanner) (DeployRow, error) {
	var d DeployRow
	err := row.Scan(&d.ID, &d.UserID, &d.UserEmail, &d.ProjectID, &d.ProjectSlug, &d.SourceType,
		&d.RepoURL, &d.Branch, &d.CommitSHA, &d.Status,
		&d.ImageRef, &d.EndpointURL, &d.Subdomain, &d.ContainerID, &d.CostVibecoins,
		&d.FailureReason, &d.TTLExpiresAt, &d.LastRequestAt,
		&d.CreatedAt, &d.UpdatedAt, &d.StoppedAt)
	return d, err
}

// ListDeploys returns one page of deploys across all accounts, newest first.
func (r *Repository) ListDeploys(ctx context.Context, f Filter) (*Page[DeployRow], error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*)`+deployJoins+deployFilter, f.Status, f.UserID, f.Query,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count deploys: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT`+deployColumns+deployJoins+deployFilter+`
		 ORDER BY d.created_at DESC
		 LIMIT $4 OFFSET $5`,
		f.Status, f.UserID, f.Query, f.Limit, f.Offset,
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
	d, err := scanDeploy(r.pool.QueryRow(ctx,
		`SELECT`+deployColumns+deployJoins+` WHERE d.id = $1`, deployID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get deploy: %w", err)
	}

	detail := &DeployDetail{DeployRow: d, Domains: []DomainRow{}, Ledger: []LedgerRow{}}

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

	ledger, err := r.listLedgerForDeploy(ctx, deployID)
	if err != nil {
		return nil, err
	}
	detail.Ledger = ledger

	return detail, nil
}

// getSaga returns the saga row for a deploy, or nil when none exists. A deploy
// created before the saga path, or one that never reached the worker, has no
// saga — that absence is information, not an error.
func (r *Repository) getSaga(ctx context.Context, deployID uuid.UUID) (*SagaRow, error) {
	var s SagaRow
	err := r.pool.QueryRow(ctx, `
		SELECT deploy_id, current_step, coins_reserved, image_built, container_running,
		       coins_committed, retry_count, failure_reason, last_error,
		       created_at, updated_at, started_at, completed_at
		FROM deploy_sagas WHERE deploy_id = $1`, deployID,
	).Scan(&s.DeployID, &s.CurrentStep, &s.CoinsReserved, &s.ImageBuilt, &s.ContainerRunning,
		&s.CoinsCommitted, &s.RetryCount, &s.FailureReason, &s.LastError,
		&s.CreatedAt, &s.UpdatedAt, &s.StartedAt, &s.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get saga: %w", err)
	}
	return &s, nil
}

const ledgerColumns = `
    t.id, t.user_id, u.email, t.deploy_id, t.type, t.amount, t.status,
    t.created_at, t.completed_at`

const ledgerJoins = `
FROM transactions t
LEFT JOIN users u ON u.id = t.user_id`

const ledgerFilter = `
WHERE ($1 = '' OR t.type = $1)
  AND ($2::uuid IS NULL OR t.user_id = $2)
  AND ($3 = '' OR t.status = $3)`

func scanLedger(row rowScanner) (LedgerRow, error) {
	var l LedgerRow
	err := row.Scan(&l.ID, &l.UserID, &l.UserEmail, &l.DeployID, &l.Type, &l.Amount,
		&l.Status, &l.CreatedAt, &l.CompletedAt)
	return l, err
}

// ListTransactions returns one page of the ledger, newest first.
func (r *Repository) ListTransactions(ctx context.Context, f Filter) (*Page[LedgerRow], error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*)`+ledgerJoins+ledgerFilter, f.Type, f.UserID, f.Status,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count transactions: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT`+ledgerColumns+ledgerJoins+ledgerFilter+`
		 ORDER BY t.created_at DESC
		 LIMIT $4 OFFSET $5`,
		f.Type, f.UserID, f.Status, f.Limit, f.Offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	items := []LedgerRow{}
	for rows.Next() {
		l, err := scanLedger(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transaction row: %w", err)
		}
		items = append(items, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction rows: %w", err)
	}

	return &Page[LedgerRow]{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

func (r *Repository) listLedgerForDeploy(ctx context.Context, deployID uuid.UUID) ([]LedgerRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT`+ledgerColumns+ledgerJoins+`
		 WHERE t.deploy_id = $1 ORDER BY t.created_at ASC`, deployID)
	if err != nil {
		return nil, fmt.Errorf("list deploy ledger: %w", err)
	}
	defer rows.Close()

	items := []LedgerRow{}
	for rows.Next() {
		l, err := scanLedger(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy ledger row: %w", err)
		}
		items = append(items, l)
	}
	return items, rows.Err()
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
		&d.Status, &d.LastError, &d.VerifiedAt, &d.LastCheckedAt, &d.CreatedAt)
	return d, err
}

// ListDomains returns one page of custom domains, newest first.
func (r *Repository) ListDomains(ctx context.Context, f Filter) (*Page[DomainRow], error) {
	const filter = `
WHERE ($1 = '' OR cd.status = $1)
  AND ($2::uuid IS NULL OR cd.user_id = $2)
  AND ($3 = '' OR cd.domain ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')`

	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*)`+domainJoins+filter, f.Status, f.UserID, f.Query,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count domains: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT`+domainColumns+domainJoins+filter+`
		 ORDER BY cd.created_at DESC
		 LIMIT $4 OFFSET $5`,
		f.Status, f.UserID, f.Query, f.Limit, f.Offset,
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
	rows, err := r.pool.Query(ctx,
		`SELECT`+domainColumns+domainJoins+`
		 WHERE cd.target_deploy_id = $1 ORDER BY cd.domain`, deployID)
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
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.user_id, p.slug, p.source_key,
		       (SELECT count(*) FROM deploys d WHERE d.project_id = p.id),
		       p.created_at
		FROM projects p
		WHERE p.user_id = $1
		ORDER BY p.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	items := []ProjectRow{}
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.UserID, &p.Slug, &p.SourceKey, &p.DeploysCount, &p.CreatedAt); err != nil {
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
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, key_prefix, name, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	items := []APIKeyRow{}
	for rows.Next() {
		var k APIKeyRow
		if err := rows.Scan(&k.ID, &k.UserID, &k.Prefix, &k.Name, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, fmt.Errorf("scan api key row: %w", err)
		}
		items = append(items, k)
	}
	return items, rows.Err()
}
