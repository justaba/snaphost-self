// Package config provides application configuration loaded from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds container-runtime configuration.
type Config struct {
	// DomainSuffix is appended to subdomains to form full hostnames
	// (e.g. "localhost" in dev → proj-abc123.localhost).
	DomainSuffix string
	// DockerSocket is the path to the Docker daemon socket.
	DockerSocket string
	// ContainerCPULimit is the number of CPU cores allocated per user container.
	ContainerCPULimit float64
	// ContainerMemoryMB is the memory limit in megabytes per user container.
	ContainerMemoryMB int64
	// ContainerDefaultTTLMin is the default TTL in minutes for user containers.
	ContainerDefaultTTLMin int
	// WatchdogIntervalSec is the interval in seconds between watchdog sweep cycles.
	WatchdogIntervalSec int
	// AllowedImagePrefixes lists what an image_ref may start with. There is no
	// registry any more, so these are local image names: the build tags what it
	// produces "snaphost/proj-<hash>", and this is what stops the internal
	// runtime from starting an unexpected host image.
	//
	// Empty + StrictImageValidation=true is a fatal startup error.
	AllowedImagePrefixes []string

	// StrictImageValidation toggles the user_id and status cross-checks
	// against the control-plane repository. It must be true in production;
	// false is acceptable in development to skip the lookup.
	StrictImageValidation bool

	// RuntimeProbeEnabled controls whether a deploy must answer on the
	// injected port before it is reported running (Task 15b). Turning it off
	// makes "running" mean started rather than confirmed serving.
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
		DomainSuffix: envOrDefault("DOMAIN_SUFFIX", "localhost"),
		DockerSocket: envOrDefault("DOCKER_SOCKET", "/var/run/docker.sock"),
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

	prefixesRaw := os.Getenv("ALLOWED_IMAGE_PREFIXES")
	if prefixesRaw != "" {
		for _, p := range strings.Split(prefixesRaw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.AllowedImagePrefixes = append(cfg.AllowedImagePrefixes, p)
			}
		}
	}
	// Secure default: strict mode without an allow-list is a misconfiguration
	// in prod. Fail at startup rather than silently accept any image_ref.
	if cfg.StrictImageValidation && len(cfg.AllowedImagePrefixes) == 0 {
		return nil, fmt.Errorf("config: ALLOWED_IMAGE_PREFIXES must be set when STRICT_IMAGE_VALIDATION=true")
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
