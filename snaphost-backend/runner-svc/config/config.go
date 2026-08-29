// Package config provides application configuration loaded from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration values for the runner-svc service.
type Config struct {
	// Port is the HTTP listen port for the API server.
	Port string
	// RunnerBackend selects the execution backend. Docker is the only one;
	// the switch survives because backend.Backend is the seam a future
	// runtime would land on (ADR 0004).
	RunnerBackend string
	// RedisURL is the connection string for the Redis instance used for log pub/sub.
	RedisURL string
	// UserBillingURL is the base URL for the user-billing internal API.
	UserBillingURL string
	// WebhookSecret is the shared secret for service-to-service authentication.
	WebhookSecret string
	// DomainSuffix is appended to subdomains to form full hostnames
	// (e.g. "localhost" in dev → proj-abc123.localhost).
	DomainSuffix string
	// DockerSocket is the path to the Docker daemon socket (docker backend only).
	DockerSocket string
	// ContainerCPULimit is the number of CPU cores allocated per user container.
	ContainerCPULimit float64
	// ContainerMemoryMB is the memory limit in megabytes per user container.
	ContainerMemoryMB int64
	// ContainerDefaultTTLMin is the default TTL in minutes for user containers.
	ContainerDefaultTTLMin int
	// WatchdogIntervalSec is the interval in seconds between watchdog sweep cycles.
	WatchdogIntervalSec int
	// LogLevel controls the zap logger verbosity ("info" or "debug").
	LogLevel string
	// RegistryAuth is an optional base64-encoded Docker auth JSON for private registries.
	RegistryAuth string
	// AllowedRegistryPrefixes lists registry URL prefixes that image_ref
	// values are permitted to start with. Provider-agnostic: each backend
	// contributes its own values via the REGISTRY_ALLOWED_PREFIXES env var
	// (comma-separated, e.g. "host.docker.internal:5000/snaphost").
	// Empty + StrictImageValidation=true is a fatal startup error.
	AllowedRegistryPrefixes []string

	// StrictImageValidation toggles the user_id and status cross-checks
	// against user-billing in runner-svc.Service.Deploy. Must be true in
	// prod; false acceptable in dev to skip the billing roundtrip.
	StrictImageValidation bool

	// RuntimeProbeEnabled controls whether a deploy must answer on the
	// injected port before it is reported running (Task 15b). Turning it off
	// restores the pre-15 meaning of "running" — started, not serving — and
	// with it the possibility of billing a dead URL.
	RuntimeProbeEnabled bool
	// RuntimeProbeTimeoutSec bounds the probe. A scale-to-zero container's
	// first request includes a cold start, so this has to outlast one.
	RuntimeProbeTimeoutSec int
}

// TraefikNetwork is the Docker network name shared with Traefik for routing.
const TraefikNetwork = "snaphost-net"

// Load reads configuration from environment variables with fallback to defaults.
// Required fields without defaults cause an error if unset.
func Load() (*Config, error) {
	cfg := &Config{
		Port:           envOrDefault("PORT", "8084"),
		RunnerBackend:  envOrDefault("RUNNER_BACKEND", "docker"),
		UserBillingURL: envOrDefault("USER_BILLING_URL", "http://user-billing:8081"),
		DomainSuffix:   envOrDefault("DOMAIN_SUFFIX", "localhost"),
		DockerSocket:   envOrDefault("DOCKER_SOCKET", "/var/run/docker.sock"),
		LogLevel:       envOrDefault("LOG_LEVEL", "info"),
		RegistryAuth:   os.Getenv("REGISTRY_AUTH"),
	}

	// Required: REDIS_URL
	cfg.RedisURL = os.Getenv("REDIS_URL")
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("config: REDIS_URL is required but not set")
	}

	// Required: WEBHOOK_SECRET
	cfg.WebhookSecret = os.Getenv("WEBHOOK_SECRET")
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config: WEBHOOK_SECRET is required but not set")
	}

	// Validate RunnerBackend
	switch cfg.RunnerBackend {
	case "docker":
		// valid
	default:
		return nil, fmt.Errorf("config: RUNNER_BACKEND must be 'docker', got %q", cfg.RunnerBackend)
	}

	// Optional: CONTAINER_CPU_LIMIT (default 0.5)
	cpuStr := envOrDefault("CONTAINER_CPU_LIMIT", "0.5")
	cpuLimit, err := strconv.ParseFloat(cpuStr, 64)
	if err != nil {
		return nil, fmt.Errorf("config: CONTAINER_CPU_LIMIT is not a valid float: %w", err)
	}
	cfg.ContainerCPULimit = cpuLimit

	// Optional: CONTAINER_MEMORY_MB (default 512)
	memStr := envOrDefault("CONTAINER_MEMORY_MB", "512")
	memMB, err := strconv.ParseInt(memStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("config: CONTAINER_MEMORY_MB is not a valid integer: %w", err)
	}
	cfg.ContainerMemoryMB = memMB

	// Optional: CONTAINER_DEFAULT_TTL_MIN (default 30)
	ttlStr := envOrDefault("CONTAINER_DEFAULT_TTL_MIN", "30")
	ttlMin, err := strconv.Atoi(ttlStr)
	if err != nil {
		return nil, fmt.Errorf("config: CONTAINER_DEFAULT_TTL_MIN is not a valid integer: %w", err)
	}
	cfg.ContainerDefaultTTLMin = ttlMin

	// Image validation config.
	strictStr := envOrDefault("STRICT_IMAGE_VALIDATION", "true")
	strict, err := strconv.ParseBool(strictStr)
	if err != nil {
		return nil, fmt.Errorf("config: STRICT_IMAGE_VALIDATION is not a valid bool: %w", err)
	}
	cfg.StrictImageValidation = strict

	prefixesRaw := os.Getenv("REGISTRY_ALLOWED_PREFIXES")
	if prefixesRaw != "" {
		for _, p := range strings.Split(prefixesRaw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.AllowedRegistryPrefixes = append(cfg.AllowedRegistryPrefixes, p)
			}
		}
	}
	// Secure default: strict mode without an allow-list is a misconfiguration
	// in prod. Fail at startup rather than silently accept any image_ref.
	if cfg.StrictImageValidation && len(cfg.AllowedRegistryPrefixes) == 0 {
		return nil, fmt.Errorf("config: REGISTRY_ALLOWED_PREFIXES must be set when STRICT_IMAGE_VALIDATION=true")
	}

	// Optional: WATCHDOG_INTERVAL_SEC (default 30)
	wdStr := envOrDefault("WATCHDOG_INTERVAL_SEC", "30")
	wdSec, err := strconv.Atoi(wdStr)
	if err != nil {
		return nil, fmt.Errorf("config: WATCHDOG_INTERVAL_SEC is not a valid integer: %w", err)
	}
	cfg.WatchdogIntervalSec = wdSec

	// Optional: RUNTIME_PROBE_ENABLED (default true — a deploy that does not
	// answer must never be reported running).
	probeStr := envOrDefault("RUNTIME_PROBE_ENABLED", "true")
	probeEnabled, err := strconv.ParseBool(probeStr)
	if err != nil {
		return nil, fmt.Errorf("config: RUNTIME_PROBE_ENABLED is not a valid bool: %w", err)
	}
	cfg.RuntimeProbeEnabled = probeEnabled

	// Optional: RUNTIME_PROBE_TIMEOUT_SEC (default 30 — has to outlast a
	// serverless cold start).
	probeTimeoutStr := envOrDefault("RUNTIME_PROBE_TIMEOUT_SEC", "30")
	probeTimeout, err := strconv.Atoi(probeTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("config: RUNTIME_PROBE_TIMEOUT_SEC is not a valid integer: %w", err)
	}
	if probeTimeout <= 0 {
		return nil, fmt.Errorf("config: RUNTIME_PROBE_TIMEOUT_SEC must be positive")
	}
	cfg.RuntimeProbeTimeoutSec = probeTimeout

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
