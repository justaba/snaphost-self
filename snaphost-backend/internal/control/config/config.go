// Package config provides application configuration loaded from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds control-plane configuration.
type Config struct {
	// DatabasePath is where the SQLite file lives. A directory that does not
	// exist yet is created — a fresh install points this at an empty volume.
	DatabasePath string
	// RunMigrations controls whether database migrations run automatically at startup.
	RunMigrations bool

	// OperatorEmail is the address the first-start operator account is created
	// under. It is only read when no account has a password yet, so changing it
	// later renames nothing — the account already exists.
	OperatorEmail string
	// SessionTTLHours is how long a session lives. It slides forward while the
	// session is in use, so this is an idle timeout rather than a hard cap.
	SessionTTLHours int
	// SessionCookieSecure forces the Secure attribute on the session cookie on
	// or off. Nil — the variable unset — derives it from the request, which is
	// what a fresh install needs: the operator reaches a new box over plain
	// HTTP to log in and put a certificate on it, and a cookie the browser
	// refuses to send makes that impossible.
	SessionCookieSecure *bool

	// SagaBuildTimeoutMin caps how long a saga waits for the build pipeline to
	// emit a build event before treating the build as failed.
	SagaBuildTimeoutMin int
	// SagaResumeIntervalSec controls how often the resume sweeper looks
	// for stuck sagas and re-enqueues them.
	SagaResumeIntervalSec int
	// DeployDefaultPort is the container port the runtime tells Traefik
	// to load-balance to. Should match the port the user app listens on.
	// Most templates use 3000 (Node) or 8080 (older Node, Java, generic).
	DeployDefaultPort int
	// MaxUploadSizeMB bounds the archive body accepted by
	// POST /api/v1/deploys/upload.
	MaxUploadSizeMB int
	// UploadTTLMin is how long an uploaded archive lives on disk
	// before the expiry sweep deletes it.
	UploadTTLMin int
	// GitCredTTLMin is how long a git_private credential stays in memory.
	// Must exceed SagaBuildTimeoutMin so a slow build can still clone.
	GitCredTTLMin int

	// ReservedDomains are the platform's other zones, above all the
	// control-plane domain the dashboard and API answer on. User deploys
	// live on a different registrable domain from sessions on purpose —
	// otherwise a deployed app could set cookies on the domain the
	// dashboard authenticates against. Attaching them is refused.
	ReservedDomains []string

	// DeployTTLMin is the TTL sent to the runtime for a new deploy. It is a
	// tier value, not a runtime constant: the runtime applies whatever
	// non-zero ttl_minutes it receives with no upper bound, so the tier's
	// limit has to be applied here, where the tier is known.
	DeployTTLMin int
	// DeployTTLMaxMin is the ceiling this service enforces on DeployTTLMin.
	DeployTTLMaxMin int

	// StoppedImageGraceHours is how long a stopped deploy keeps the image it
	// can be started from before the watchdog gives up on it and reclaims the
	// disk. Zero disables the sweep, which means stopped images are kept
	// indefinitely.
	StoppedImageGraceHours int
	// ProjectDeployRetention is how many superseded deploys per project
	// stay running as rollback targets.
	ProjectDeployRetention int

	// MaxDomainsPerUser bounds custom domains. Zero means unlimited, which is
	// the self-host default so every project can have its own domain.
	MaxDomainsPerUser int
	// DomainAttachRequireIdentity gates domain attach on a payment-verified
	// account. Default off: payment collection does not exist yet, so the
	// gate ships as manual abuse review rather than a hard refusal.
	DomainAttachRequireIdentity bool
	// DomainAttachPerHour bounds attach attempts per user per hour.
	DomainAttachPerHour int
	// DomainVerifyIntervalSec is how often the TXT verification sweep runs.
	DomainVerifyIntervalSec int
	// DomainReverifyHours is how stale a verified domain's last successful
	// check may become before it is re-checked.
	DomainReverifyHours int
	// DomainVerifyGraceHours is how long a newly attached domain may go without
	// its TXT record before that counts as a failure rather than as DNS still
	// propagating. Inside the window the domain stays "pending", which is what
	// is actually true and what the dashboard should show.
	DomainVerifyGraceHours int
	// DomainCNAMETarget and DomainARecordTarget are the published DNS
	// targets users point at. Both empty means no stable public address has
	// been reserved yet and attach is refused — publishing an address that
	// later moves breaks every customer's site at once.
	DomainCNAMETarget   string
	DomainARecordTarget string
}

// Load reads configuration from environment variables with fallback to defaults.
// Required fields without defaults cause an error if unset.
func Load() (*Config, error) {
	cfg := &Config{}

	// Optional: DATABASE_PATH. The default sits under a directory a container
	// can own, so the common case needs no configuration at all.
	cfg.DatabasePath = envOrDefault("DATABASE_PATH", "/var/snaphost/data/snaphost.db")

	// Optional: RUN_MIGRATIONS (default true)
	runMigrations, err := parseBoolEnv("RUN_MIGRATIONS", true)
	if err != nil {
		return nil, err
	}
	cfg.RunMigrations = runMigrations

	// Optional: OPERATOR_EMAIL, SESSION_TTL_HOURS (default 168 = one week),
	// SESSION_COOKIE_SECURE (unset = derive from the request).
	cfg.OperatorEmail = envOrDefault("OPERATOR_EMAIL", "operator@localhost")
	sessionTTL, err := parseIntEnv("SESSION_TTL_HOURS", 168)
	if err != nil {
		return nil, err
	}
	if sessionTTL <= 0 {
		return nil, fmt.Errorf("config: SESSION_TTL_HOURS must be positive, got %d", sessionTTL)
	}
	cfg.SessionTTLHours = sessionTTL

	cookieSecure, err := parseOptionalBoolEnv("SESSION_COOKIE_SECURE")
	if err != nil {
		return nil, err
	}
	cfg.SessionCookieSecure = cookieSecure

	// Optional: SAGA_BUILD_TIMEOUT_MIN (default 15)
	buildTimeout, err := parseIntEnv("SAGA_BUILD_TIMEOUT_MIN", 15)
	if err != nil {
		return nil, err
	}
	cfg.SagaBuildTimeoutMin = buildTimeout

	// Optional: SAGA_RESUME_INTERVAL_SEC (default 60)
	resumeInterval, err := parseIntEnv("SAGA_RESUME_INTERVAL_SEC", 60)
	if err != nil {
		return nil, err
	}
	cfg.SagaResumeIntervalSec = resumeInterval

	// Optional: DEPLOY_DEFAULT_PORT (default 3000)
	port, err := parseIntEnv("DEPLOY_DEFAULT_PORT", 3000)
	if err != nil {
		return nil, err
	}
	cfg.DeployDefaultPort = port

	// Optional: MAX_UPLOAD_SIZE_MB (default 50)
	maxUpload, err := parseIntEnv("MAX_UPLOAD_SIZE_MB", 50)
	if err != nil {
		return nil, err
	}
	cfg.MaxUploadSizeMB = maxUpload

	// Optional: UPLOAD_TTL_MIN (default 15)
	uploadTTL, err := parseIntEnv("UPLOAD_TTL_MIN", 15)
	if err != nil {
		return nil, err
	}
	cfg.UploadTTLMin = uploadTTL

	// Optional: GIT_CRED_TTL_MIN (default 30 — must outlive a build)
	credTTL, err := parseIntEnv("GIT_CRED_TTL_MIN", 30)
	if err != nil {
		return nil, err
	}
	cfg.GitCredTTLMin = credTTL

	for _, zone := range strings.Split(os.Getenv("RESERVED_DOMAINS"), ",") {
		if zone = strings.TrimSpace(zone); zone != "" {
			cfg.ReservedDomains = append(cfg.ReservedDomains, zone)
		}
	}
	cfg.DomainCNAMETarget = envOrDefault("DOMAIN_CNAME_TARGET", "")
	cfg.DomainARecordTarget = envOrDefault("DOMAIN_A_RECORD_TARGET", "")

	intVars := []struct {
		key      string
		fallback int
		out      *int
	}{
		// 1440 = 24h. Under scale-to-zero an idle deploy costs approximately
		// nothing, and a link that survives the working day is the difference
		// between a shareable preview and one that expires before anyone
		// opens it. Local Docker dev keeps a short TTL in compose, where a
		// container really does hold host RAM for its whole TTL.
		{"DEPLOY_TTL_MIN", 1440, &cfg.DeployTTLMin},
		{"DEPLOY_TTL_MAX_MIN", 1440, &cfg.DeployTTLMaxMin},
		{"PROJECT_DEPLOY_RETENTION", 3, &cfg.ProjectDeployRetention},
		// A stopped deploy keeps the image it can be restarted from, which is
		// what makes starting one a container run rather than a rebuild. The
		// favour has to expire or every expired preview leaks a build's worth
		// of disk forever. A week is long enough that restarting last week's
		// site still works and short enough that the host does not fill.
		{"STOPPED_IMAGE_GRACE_HOURS", 168, &cfg.StoppedImageGraceHours},
		{"MAX_DOMAINS_PER_USER", 0, &cfg.MaxDomainsPerUser},
		{"DOMAIN_ATTACH_PER_HOUR", 5, &cfg.DomainAttachPerHour},
		{"DOMAIN_VERIFY_INTERVAL_SEC", 60, &cfg.DomainVerifyIntervalSec},
		{"DOMAIN_REVERIFY_HOURS", 24, &cfg.DomainReverifyHours},
		{"DOMAIN_VERIFY_GRACE_HOURS", 24, &cfg.DomainVerifyGraceHours},
	}
	for _, v := range intVars {
		parsed, err := parseIntEnv(v.key, v.fallback)
		if err != nil {
			return nil, err
		}
		*v.out = parsed
	}

	requireIdentity, err := parseBoolEnv("DOMAIN_ATTACH_REQUIRE_IDENTITY", false)
	if err != nil {
		return nil, err
	}
	cfg.DomainAttachRequireIdentity = requireIdentity

	return cfg, nil
}

// envOrDefault returns the value of the named environment variable or the
// provided fallback if the variable is empty or unset.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseIntEnv(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not a valid integer: %w", key, err)
	}
	return v, nil
}

// parseOptionalBoolEnv distinguishes "unset" from "set to false", which
// parseBoolEnv cannot: a nil result is a caller's cue to decide for itself.
func parseOptionalBoolEnv(key string) (*bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("config: %s is not a valid boolean: %w", key, err)
	}
	return &v, nil
}

func parseBoolEnv(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s is not a valid boolean: %w", key, err)
	}
	return v, nil
}
