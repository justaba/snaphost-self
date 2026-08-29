// Package config provides application configuration loaded from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration values for the user-billing service.
type Config struct {
	// Port is the HTTP listen port for the service.
	Port string
	// DatabaseURL is the full PostgreSQL connection string (e.g. postgres://user:pass@host:5432/db).
	DatabaseURL string
	// RedisURL is the Redis connection string used by the saga queue and
	// the build-events subscription. Optional — if empty, the saga
	// worker is disabled regardless of SagaWorkerEnabled.
	RedisURL string
	// LogLevel controls the zap logger verbosity ("info" or "debug").
	LogLevel string
	// WebhookSecret is the shared secret used by api-gateway and other
	// internal services for authenticating webhook calls.
	WebhookSecret string
	// RunMigrations controls whether database migrations run automatically at startup.
	RunMigrations bool

	// BuilderSvcURL is the base URL of builder-svc's internal API. Used
	// by the saga orchestrator to enqueue builds.
	BuilderSvcURL string
	// RunnerSvcURL is the base URL of runner-svc's internal API. Used
	// by the saga orchestrator to start and stop containers.
	RunnerSvcURL string
	// SagaWorkerEnabled toggles in-process saga worker startup. Disable
	// when running the worker as a separate process.
	SagaWorkerEnabled bool
	// SagaBuildTimeoutMin caps how long a saga waits for builder-svc to
	// emit a build event before treating the build as failed.
	SagaBuildTimeoutMin int
	// SagaResumeIntervalSec controls how often the resume sweeper looks
	// for stuck sagas and re-enqueues them.
	SagaResumeIntervalSec int
	// DeployDefaultPort is the container port runner-svc tells Traefik
	// to load-balance to. Should match the port the user app listens on.
	// Most templates use 3000 (Node) or 8080 (older Node, Java, generic).
	DeployDefaultPort int
	// MaxUploadSizeMB bounds the archive body accepted by
	// POST /api/v1/deploys/upload. Must not exceed what api-gateway
	// allows for the same route (MAX_UPLOAD_SIZE_MB there too).
	MaxUploadSizeMB int
	// UploadTTLMin is how long an uploaded archive blob lives in Redis
	// before the TTL backstop deletes it.
	UploadTTLMin int
	// GitCredTTLMin is how long a git_private credential lives in Redis.
	// Must exceed SagaBuildTimeoutMin so a slow build can still clone.
	GitCredTTLMin int

	// DomainSuffix is the platform's own runtime domain suffix. Route
	// lookups for hosts under it resolve through deploys.subdomain;
	// anything else is treated as a custom domain. It is also the
	// namespace users are refused when attaching a domain.
	DomainSuffix string
	// ReservedDomains are the platform's other zones, above all the
	// control-plane domain the dashboard and API answer on. User deploys
	// live on a different registrable domain from sessions on purpose —
	// otherwise a deployed app could set cookies on the domain the
	// dashboard authenticates against. Attaching them is refused.
	ReservedDomains []string

	// DeployTTLMin is the TTL sent to runner-svc for a new deploy. It is a
	// tier value, not a runtime constant: runner-svc applies whatever
	// non-zero ttl_minutes it receives with no upper bound, so the tier's
	// limit has to be applied here, where the tier is known.
	DeployTTLMin int
	// DeployTTLMaxMin is the ceiling this service enforces on DeployTTLMin.
	DeployTTLMaxMin int

	// AliasIdleGCDays is how long an alias-pinned deploy may serve no
	// traffic before the alias is unpinned and the runtime reclaimed.
	AliasIdleGCDays int
	// ProjectDeployRetention is how many superseded deploys per project
	// stay running as rollback targets.
	ProjectDeployRetention int

	// MaxDomainsPerUser is the per-tier custom domain count (free tier: 1).
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
	cfg := &Config{
		Port:     envOrDefault("PORT", "8081"),
		LogLevel: envOrDefault("LOG_LEVEL", "info"),
		RedisURL: envOrDefault("REDIS_URL", ""),

		BuilderSvcURL: envOrDefault("BUILDER_SVC_URL", "http://builder-api:8082"),
		RunnerSvcURL:  envOrDefault("RUNNER_SVC_URL", "http://runner-api:8084"),
	}

	// Required: DATABASE_URL
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required but not set")
	}

	// Required: WEBHOOK_SECRET
	cfg.WebhookSecret = os.Getenv("WEBHOOK_SECRET")
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config: WEBHOOK_SECRET is required but not set")
	}

	// Optional: RUN_MIGRATIONS (default true)
	runMigrations, err := parseBoolEnv("RUN_MIGRATIONS", true)
	if err != nil {
		return nil, err
	}
	cfg.RunMigrations = runMigrations

	// Optional: SAGA_WORKER_ENABLED (default true)
	sagaEnabled, err := parseBoolEnv("SAGA_WORKER_ENABLED", true)
	if err != nil {
		return nil, err
	}
	cfg.SagaWorkerEnabled = sagaEnabled

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

	cfg.DomainSuffix = envOrDefault("DOMAIN_SUFFIX", "")
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
		{"ALIAS_IDLE_GC_DAYS", 30, &cfg.AliasIdleGCDays},
		{"PROJECT_DEPLOY_RETENTION", 3, &cfg.ProjectDeployRetention},
		{"MAX_DOMAINS_PER_USER", 1, &cfg.MaxDomainsPerUser},
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

func parseInt64Env(key string, fallback int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not a valid integer: %w", key, err)
	}
	return v, nil
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
