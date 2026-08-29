// Package admin provides the operator-facing read surface over the control
// plane's own tables: accounts, wallets, the transaction ledger, deploys and
// their saga state, projects, custom domains, and API keys.
//
// It is deliberately read-only. Every type here is shaped for one screen
// rather than reusing another package's model, because an admin list needs
// joins (an account's email beside its deploy) that the owning packages have
// no reason to carry.
//
// Two rules hold for everything in this package:
//
//   - No secret material is selectable. api_keys.key_hash and the verification
//     tokens of unverified domains never appear in a query here.
//   - Nothing writes. Operator actions belong in a later pass with their own
//     audit trail; a read surface that can also mutate has no safe default.
package admin

import (
	"time"

	"github.com/google/uuid"
)

// Overview is the set of platform-wide counters the admin dashboard opens on.
type Overview struct {
	Users          int64 `json:"users"`
	Wallets        int64 `json:"wallets"`
	TotalBalance   int64 `json:"total_balance"`
	TotalReserved  int64 `json:"total_reserved"`
	Deploys        int64 `json:"deploys"`
	DeploysRunning int64 `json:"deploys_running"`
	DeploysFailed  int64 `json:"deploys_failed"`
	Deploys24h     int64 `json:"deploys_24h"`
	Projects       int64 `json:"projects"`
	DomainsPending int64 `json:"domains_pending"`
	DomainsActive  int64 `json:"domains_verified"`
	ActiveAPIKeys  int64 `json:"active_api_keys"`
	// CoinsToppedUp and CoinsSpent are lifetime totals over completed
	// transactions. Reserved-but-uncommitted coins are in neither.
	CoinsToppedUp int64 `json:"coins_topped_up"`
	CoinsSpent    int64 `json:"coins_spent"`
	// WalletlessUsers counts accounts the control plane knows about that have
	// no wallet. It is normally zero; a non-zero value means the Supabase seed
	// webhook is not firing, and those users cannot deploy at all.
	WalletlessUsers int64 `json:"walletless_users"`
}

// UserSummary is one row of the admin user list: who they are, what they hold,
// and what they have done, in the shape the list renders.
type UserSummary struct {
	ID             uuid.UUID  `json:"id"`
	Email          *string    `json:"email,omitempty"`
	Balance        *int64     `json:"balance,omitempty"`
	Reserved       *int64     `json:"reserved,omitempty"`
	HasWallet      bool       `json:"has_wallet"`
	DeploysTotal   int64      `json:"deploys_total"`
	DeploysRunning int64      `json:"deploys_running"`
	DeploysFailed  int64      `json:"deploys_failed"`
	CoinsToppedUp  int64      `json:"coins_topped_up"`
	CoinsSpent     int64      `json:"coins_spent"`
	DomainsCount   int64      `json:"domains_count"`
	LastDeployAt   *time.Time `json:"last_deploy_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// UserDetail is a single account with everything attached to it that fits on
// one screen. The lists it carries are bounded; the paginated endpoints are
// where an operator goes for the full history.
type UserDetail struct {
	UserSummary
	Projects []ProjectRow `json:"projects"`
	Domains  []DomainRow  `json:"domains"`
	APIKeys  []APIKeyRow  `json:"api_keys"`
}

// DeployRow is one deploy as the operator sees it — the deploy's own fields
// plus the account it belongs to, which is the column an admin list is read by.
type DeployRow struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	UserEmail     *string    `json:"user_email,omitempty"`
	ProjectID     *uuid.UUID `json:"project_id,omitempty"`
	ProjectSlug   *string    `json:"project_slug,omitempty"`
	SourceType    string     `json:"source_type"`
	RepoURL       string     `json:"repo_url"`
	Branch        string     `json:"branch"`
	CommitSHA     *string    `json:"commit_sha,omitempty"`
	Status        string     `json:"status"`
	ImageRef      *string    `json:"image_ref,omitempty"`
	EndpointURL   *string    `json:"endpoint_url,omitempty"`
	Subdomain     *string    `json:"subdomain,omitempty"`
	ContainerID   *string    `json:"container_id,omitempty"`
	CostVibecoins int64      `json:"cost_vibecoins"`
	FailureReason *string    `json:"failure_reason,omitempty"`
	TTLExpiresAt  *time.Time `json:"ttl_expires_at,omitempty"`
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
}

// SagaRow is the saga's internal view of a deploy. deploys.status is what the
// user sees; this is what actually happened, including the step it stopped on
// and whether compensation ran.
type SagaRow struct {
	DeployID         uuid.UUID  `json:"deploy_id"`
	CurrentStep      string     `json:"current_step"`
	CoinsReserved    bool       `json:"coins_reserved"`
	ImageBuilt       bool       `json:"image_built"`
	ContainerRunning bool       `json:"container_running"`
	CoinsCommitted   bool       `json:"coins_committed"`
	RetryCount       int        `json:"retry_count"`
	FailureReason    *string    `json:"failure_reason,omitempty"`
	LastError        *string    `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// DeployDetail is one deploy with the saga that produced it and the domains
// currently published on it. Those three answer nearly every "why is this
// deploy like that" question without a database session.
type DeployDetail struct {
	DeployRow
	Saga    *SagaRow    `json:"saga,omitempty"`
	Domains []DomainRow `json:"domains"`
	Ledger  []LedgerRow `json:"ledger"`
	Project *ProjectRow `json:"project,omitempty"`
}

// LedgerRow is one transaction. The admin ledger is the same table the wallet
// invariant is built on, so it is shown unaggregated: a reserve without its
// matching commit or refund is the signal worth spotting.
type LedgerRow struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	UserEmail   *string    `json:"user_email,omitempty"`
	DeployID    *uuid.UUID `json:"deploy_id,omitempty"`
	Type        string     `json:"type"`
	Amount      int64      `json:"amount"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ProjectRow is a publish target with the size of its build history.
type ProjectRow struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Slug         string    `json:"slug"`
	SourceKey    string    `json:"source_key"`
	DeploysCount int64     `json:"deploys_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// DomainRow is one custom domain. The verification token is deliberately
// absent: it proves nothing to an operator and is the one value that lets
// someone else pass the ownership check.
type DomainRow struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	UserEmail      *string    `json:"user_email,omitempty"`
	ProjectID      uuid.UUID  `json:"project_id"`
	TargetDeployID *uuid.UUID `json:"target_deploy_id,omitempty"`
	Domain         string     `json:"domain"`
	Status         string     `json:"status"`
	LastError      *string    `json:"last_error,omitempty"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// APIKeyRow is an API key's metadata. key_hash is never selected — it is the
// only stored secret in this database, and an operator has no use for it.
type APIKeyRow struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Page wraps a list response with the paging state the caller needs to ask for
// the next one. Total is a separate count query, so it is exact rather than an
// estimate — these tables are small enough for that to be cheap.
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// Filter is the query state shared by the paginated list endpoints. Zero
// values mean "no filter", so an empty Filter lists everything.
type Filter struct {
	// Query matches an email, an ID, or a repository URL depending on the
	// endpoint. Matching is case-insensitive and substring-based.
	Query string
	// UserID restricts the list to one account.
	UserID *uuid.UUID
	// Status restricts deploys, transactions, or domains to one state.
	Status string
	// Type restricts transactions to one ledger type.
	Type   string
	Limit  int
	Offset int
}
