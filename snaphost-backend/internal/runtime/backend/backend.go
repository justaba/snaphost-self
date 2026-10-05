// Package backend defines the narrow container-engine contract used by the
// runtime. Keeping Docker behind this interface makes lifecycle behavior
// independently testable.
package backend

import (
	"context"
	"errors"
	"time"
)

// RunRequest describes what to deploy.
type RunRequest struct {
	// DeployID is the internal deployment identifier used for naming and labels.
	DeployID string
	// UserID identifies the owning user for quota tracking and labels.
	UserID string
	// ImageRef is the full image reference from the container registry.
	ImageRef string
	// Env holds environment variables to pass into the container.
	Env map[string]string
	// Port is the port the user application listens on.
	Port int
	// TTL is the duration after which the container must be stopped.
	TTL time.Duration
	// Subdomain is a stable runtime identifier. Local development may use it
	// for a Traefik hostname; production publishes only verified domains.
	Subdomain string
}

// RunResult is returned once the deployment is live.
type RunResult struct {
	// ContainerID is the Docker container id.
	ContainerID string
	// EndpointURL is populated only for a local development route. Production
	// leaves it empty until a verified project domain is attached.
	EndpointURL string
	// StartedAt is the time the container started.
	StartedAt time.Time
	// TTLExpiresAt is the time at which the container should be stopped.
	TTLExpiresAt time.Time
}

// HealthStatus is returned by HealthCheck.
type HealthStatus struct {
	// Running indicates whether the deployment is currently active.
	Running bool
	// LastStarted is when the container was last started.
	LastStarted time.Time
	// Message is a human-readable status description.
	Message string
}

// ProbeRequest describes the runtime to check after it has started.
type ProbeRequest struct {
	// DeployID is used for deploy-log publishing.
	DeployID string
	// ContainerID is the handle Run returned.
	ContainerID string
	// Port is the port the application was told to listen on.
	Port int
	// EndpointURL is the public URL Run returned, for backends that can only
	// be reached through it.
	EndpointURL string
}

// Prober can reach the container it started and confirm that the configured
// application port answers.
type Prober interface {
	Probe(ctx context.Context, req ProbeRequest) error
}

// Backend is implemented by the local Docker engine.
type Backend interface {
	// Run locates the image, creates and starts the container,
	// waits for it to be reachable, and returns the public endpoint.
	Run(ctx context.Context, req RunRequest) (*RunResult, error)

	// Stop gracefully shuts down the deployment and cleans up associated resources
	// (networks, volumes, routing rules). deployID is provided for deploy-log
	// publishing; containerID is the Docker container handle.
	Stop(ctx context.Context, deployID, containerID string) error

	// HealthCheck reports whether the deployment is currently running.
	HealthCheck(ctx context.Context, containerID string) (*HealthStatus, error)

	// StreamLogs tails container stdout/stderr. The returned channel emits lines
	// until the context is cancelled or the container dies. Callers forward these
	// lines to the runtime log publisher.
	StreamLogs(ctx context.Context, containerID string) (<-chan string, error)

	// RemoveImage releases the deploy artifact from the backend's local image
	// store. It must be idempotent: cleanup retries after a crash may ask for an
	// image that was already removed.
	RemoveImage(ctx context.Context, imageRef string) error

	// Name returns the backend identifier for logging.
	Name() string
}

// Typed errors that backends must use so callers can match with errors.Is.
var (
	// ErrImageUnavailable indicates the requested image is absent locally.
	ErrImageUnavailable = errors.New("image unavailable")
	// ErrContainerStartFailed indicates the container failed to start.
	ErrContainerStartFailed = errors.New("container start failed")
	// ErrContainerNotFound indicates the container does not exist.
	ErrContainerNotFound = errors.New("container not found")
	// ErrHealthCheckTimeout indicates the container did not become healthy in time.
	ErrHealthCheckTimeout = errors.New("health check timeout")
	// ErrProbeFailed indicates the container is up but nothing answered on the
	// port the runtime injects. A deploy in this state would otherwise be
	// reported and billed as running with a dead public URL.
	ErrProbeFailed = errors.New("runtime did not answer on the injected port")
)
