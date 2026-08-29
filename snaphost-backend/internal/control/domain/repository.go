package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"database/sql"

	"github.com/google/uuid"

	controldb "snaphost/internal/control/db"
)

var (
	// ErrNotFound is returned when no matching non-revoked domain exists.
	ErrNotFound = errors.New("domain not found")
	// ErrDuplicate is returned when the hostname is already attached by
	// anyone — including this user on another project.
	ErrDuplicate = errors.New("domain already attached")
	// ErrInvalidTarget is returned when a repoint names a deploy that is not
	// a live deploy of the same project.
	ErrInvalidTarget = errors.New("deploy is not a valid alias target for this project")
)

// Repository persists custom domains in PostgreSQL.
type Repository struct {
	db *sql.DB
}

// NewRepository builds a Repository over the given pool.
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{db: handle}
}

const domainColumns = `id, user_id, project_id, target_deploy_id, domain, verification_token, status,
	       last_error, verified_at, last_checked_at, created_at, updated_at`

func scanDomain(row interface{ Scan(dest ...any) error }) (Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.UserID, &d.ProjectID, &d.TargetDeployID, &d.Domain, &d.VerificationToken,
		&d.Status, &d.LastError, controldb.IntoNull(&d.VerifiedAt), controldb.IntoNull(&d.LastCheckedAt),
		controldb.Into(&d.CreatedAt), controldb.Into(&d.UpdatedAt))
	return d, err
}

// Create attaches a domain to a project in pending state. The unique index on
// non-revoked rows is what makes a duplicate attach fail here rather than
// producing two rows that both claim the same hostname.
func (r *Repository) Create(ctx context.Context, userID, projectID uuid.UUID, host, token string, target *uuid.UUID) (*Domain, error) {
	d, err := scanDomain(r.db.QueryRowContext(ctx,
		`INSERT INTO custom_domains (id, user_id, project_id, target_deploy_id, domain, verification_token)
		 VALUES (?, ?, ?, ?, ?, ?)
		 RETURNING `+domainColumns,
		uuid.NewString(), userID.String(), projectID.String(), targetArg(target), host, token,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create custom domain: %w", err)
	}
	return &d, nil
}

// isUniqueViolation reports a SQLITE_CONSTRAINT_UNIQUE, which for this table
// can only mean the hostname is already attached by a non-revoked row.
//
// Matched on the message rather than a typed error: the pure-Go driver's error
// type is not part of its public API, so a string check is the stable option.
// It is narrow enough to be safe — the only unique index this statement can
// violate is uq_custom_domains_domain_active.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// targetArg renders an optional deploy id for binding, since a nil pointer has
// to reach the driver as NULL rather than as a typed nil it cannot convert.
func targetArg(target *uuid.UUID) any {
	if target == nil {
		return nil
	}
	return target.String()
}

// Get returns one domain by id, scoped to its owner.
func (r *Repository) Get(ctx context.Context, userID, id uuid.UUID) (*Domain, error) {
	d, err := scanDomain(r.db.QueryRowContext(ctx,
		`SELECT `+domainColumns+`
		 FROM custom_domains
		 WHERE id = ? AND user_id = ? AND status <> 'revoked'`,
		id.String(), userID.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get custom domain: %w", err)
	}
	return &d, nil
}

// ListByUser returns a user's attached domains, newest first.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+domainColumns+`
		 FROM custom_domains
		 WHERE user_id = ? AND status <> 'revoked'
		 ORDER BY created_at DESC`,
		userID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	defer rows.Close()

	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate custom domains: %w", err)
	}
	return out, nil
}

// CountActiveByUser counts the domains a user holds, for the per-tier limit.
func (r *Repository) CountActiveByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM custom_domains WHERE user_id = ? AND status <> 'revoked'`,
		userID.String(),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count custom domains: %w", err)
	}
	return n, nil
}

// Revoke detaches a domain: it stops resolving on the next lookup (no cache
// sits in front of route resolution today) and its target is released, so a
// deploy is never left pinned by a domain that no longer exists.
func (r *Repository) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE custom_domains
		 SET status = 'revoked', target_deploy_id = NULL, updated_at = ?
		 WHERE id = ? AND user_id = ? AND status <> 'revoked'`,
		controldb.Now(), id.String(), userID.String(),
	)
	if err != nil {
		return fmt.Errorf("revoke custom domain: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke custom domain: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTarget repoints the alias at another deploy of the same project. This is
// one atomic update, which is what makes both publishing and rollback a
// pointer move: the previous deploy answers every request until this commits.
// The deploy must be running and belong to the domain's project, so a repoint
// can never point a user's hostname at someone else's container.
func (r *Repository) SetTarget(ctx context.Context, userID, id, deployID uuid.UUID) (*Domain, error) {
	d, err := scanDomain(r.db.QueryRowContext(ctx,
		`UPDATE custom_domains AS cd
		 SET target_deploy_id = ?, last_error = NULL, updated_at = ?
		 WHERE cd.id = ? AND cd.user_id = ? AND cd.status <> 'revoked'
		   AND EXISTS (
		       SELECT 1 FROM deploys d
		       WHERE d.id = ?
		         AND d.project_id = cd.project_id
		         AND d.user_id = cd.user_id
		         AND d.status = 'running'
		         AND d.container_id IS NOT NULL
		         AND d.container_id <> ''
		   )
		 RETURNING `+domainColumns,
		deployID.String(), controldb.Now(), id.String(), userID.String(), deployID.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		// Either the domain is gone or the deploy is not a valid target;
		// distinguish so the dashboard can say which.
		if _, getErr := r.Get(ctx, userID, id); getErr != nil {
			return nil, getErr
		}
		return nil, ErrInvalidTarget
	}
	if err != nil {
		return nil, fmt.Errorf("set alias target: %w", err)
	}
	return &d, nil
}

// PromoteToDeploy points every verified domain of the deploy's project at that
// deploy, and reports how many moved.
//
// This is publishing: one statement, so a domain is never briefly pointing at
// nothing. The previous deploy keeps serving right up to it, which is what
// makes a redeploy invisible from outside and a failed build harmless — the
// caller only reaches here once the new deploy is running, and since Task 15b
// "running" means it actually answered on the injected port.
//
// Scoped to the project rather than to the domain's current target, so a domain
// that was attached but never pointed anywhere gets published by the next
// deploy instead of sitting on a 404 until someone notices.
func (r *Repository) PromoteToDeploy(ctx context.Context, deployID uuid.UUID) (int, error) {
	result, err := r.db.ExecContext(ctx,
		// `IS NOT` rather than `IS DISTINCT FROM`: SQLite's IS/IS NOT are
		// already null-safe, and the spelling works on every version.
		`UPDATE custom_domains
		 SET target_deploy_id = ?, updated_at = ?
		 WHERE status = 'verified'
		   AND target_deploy_id IS NOT ?
		   AND project_id = (SELECT project_id FROM deploys WHERE id = ?)`,
		deployID.String(), controldb.Now(), deployID.String(), deployID.String(),
	)
	if err != nil {
		return 0, fmt.Errorf("promote aliases to deploy: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("promote aliases to deploy: %w", err)
	}
	return int(affected), nil
}

// IsVerifiedHost reports whether the hostname is an attached domain that has
// proven ownership. It is the question the TLS edge asks before issuing a
// certificate, so it deliberately looks at nothing else: not the target
// deploy, not whether that deploy is running. A verified domain whose target
// is down should still hold a certificate and answer with the router's 404 —
// dropping the certificate would turn a dead page into a browser TLS warning,
// and re-issuing it later costs an ACME round trip against rate limits we do
// not control.
//
// Owner-scoped lookups elsewhere in this repository take a user ID because
// they serve a request from that user. This one answers a question about a
// hostname on behalf of the edge, which has no user, so the caller's
// authorization is the webhook secret rather than ownership.
func (r *Repository) IsVerifiedHost(ctx context.Context, host string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM custom_domains
		    WHERE domain = ? AND status = 'verified'
		 )`,
		host,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check verified host: %w", err)
	}
	return exists, nil
}

// DueForCheck returns domains the verifier should resolve: everything still
// pending, plus verified domains whose last check is older than reverifyAfter.
// Re-verification is what catches a domain that stopped pointing at us or was
// transferred to someone else — otherwise we would keep serving a hostname
// whose owner has moved on.
func (r *Repository) DueForCheck(ctx context.Context, reverifyAfter time.Duration, limit int) ([]Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+domainColumns+`
		 FROM custom_domains
		 WHERE (status = 'pending' OR status = 'failed'
		        OR (status = 'verified' AND (last_checked_at IS NULL OR last_checked_at < ?)))
		 ORDER BY last_checked_at
		 LIMIT ?`,
		controldb.FormatTime(time.Now().Add(-reverifyAfter)), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list domains due for check: %w", err)
	}
	defer rows.Close()

	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan domain due for check: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains due for check: %w", err)
	}
	return out, nil
}

// MarkVerified records a successful ownership proof.
func (r *Repository) MarkVerified(ctx context.Context, id uuid.UUID) error {
	now := controldb.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE custom_domains
		 SET status = 'verified', verified_at = ?, last_checked_at = ?,
		     last_error = NULL, updated_at = ?
		 WHERE id = ? AND status <> 'revoked'`,
		now, now, now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("mark domain verified: %w", err)
	}
	return nil
}

// MarkFailed records a failed check. A previously verified domain loses
// verification, which immediately stops it routing — a dangling record that
// now belongs to someone else must not keep being served.
func (r *Repository) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	now := controldb.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE custom_domains
		 SET status = 'failed', last_checked_at = ?, last_error = ?, updated_at = ?
		 WHERE id = ? AND status <> 'revoked'`,
		now, reason, now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("mark domain failed: %w", err)
	}
	return nil
}

// TouchChecked records a check that neither proved nor disproved ownership
// (a transient resolver error), leaving the current status alone.
func (r *Repository) TouchChecked(ctx context.Context, id uuid.UUID, reason string) error {
	now := controldb.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE custom_domains
		 SET last_checked_at = ?, last_error = ?, updated_at = ?
		 WHERE id = ? AND status <> 'revoked'`,
		now, reason, now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("touch domain check: %w", err)
	}
	return nil
}

// OwnsProject reports whether the project exists and belongs to the user.
func (r *Repository) OwnsProject(ctx context.Context, userID, projectID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE id = ? AND user_id = ?)`,
		projectID.String(), userID.String(),
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check project ownership: %w", err)
	}
	return exists, nil
}

// ProjectOfDeploy resolves the project a deploy belongs to, scoped to the
// owner, so a domain can be attached by naming a deploy instead of a project.
func (r *Repository) ProjectOfDeploy(ctx context.Context, userID, deployID uuid.UUID) (uuid.UUID, error) {
	var projectID *uuid.UUID
	err := r.db.QueryRowContext(ctx,
		`SELECT project_id FROM deploys WHERE id = ? AND user_id = ?`,
		deployID.String(), userID.String(),
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && projectID == nil) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve project of deploy: %w", err)
	}
	return *projectID, nil
}
