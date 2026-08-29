// Package docker implements the backend.Backend interface using the local Docker
// daemon. All Docker SDK calls are isolated in this package — nothing outside
// internal/backend/docker/ may import the Docker SDK.
package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"

	"snaphost/internal/runtime/backend"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/logs"
)

// DockerBackend implements backend.Backend using the local Docker daemon.
type DockerBackend struct {
	cli       *client.Client
	cfg       *config.Config
	publisher logs.Publisher
	log       *zap.Logger
}

// NewDockerBackend creates a new DockerBackend, connecting to the Docker daemon
// and verifying it is reachable. Returns an error if the daemon cannot be pinged.
func NewDockerBackend(cfg *config.Config, publisher logs.Publisher, log *zap.Logger) (*DockerBackend, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping docker daemon: %w", err)
	}

	log.Info("docker backend connected", zap.String("socket", cfg.DockerSocket))

	return &DockerBackend{
		cli:       cli,
		cfg:       cfg,
		publisher: publisher,
		log:       log,
	}, nil
}

// Run pulls the image if needed, creates and starts the container on an isolated
// network, connects it to the Traefik network, waits for it to be healthy, and
// returns the public endpoint URL.
func (b *DockerBackend) Run(ctx context.Context, req backend.RunRequest) (*backend.RunResult, error) {
	// Apply overall timeout.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	containerName := fmt.Sprintf("snaphost-deploy-%s", req.DeployID)

	// 1. Pull the image.
	b.publishLog(req.DeployID, "runtime-startup", fmt.Sprintf("pulling image %s", req.ImageRef), "info")

	pullOpts := image.PullOptions{}
	if b.cfg.RegistryAuth != "" {
		pullOpts.RegistryAuth = b.cfg.RegistryAuth
	}

	pullReader, err := b.cli.ImagePull(ctx, req.ImageRef, pullOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", backend.ErrImagePullFailed, err.Error())
	}
	// Drain the pull stream so Docker actually pulls the layers.
	if _, err := io.Copy(io.Discard, pullReader); err != nil {
		pullReader.Close()
		return nil, fmt.Errorf("%w: drain pull stream: %s", backend.ErrImagePullFailed, err.Error())
	}
	pullReader.Close()

	b.publishLog(req.DeployID, "runtime-startup", "image pulled successfully", "info")

	// 2. Create isolated network.
	if _, err := CreateIsolatedNetwork(ctx, b.cli, req.DeployID); err != nil {
		return nil, fmt.Errorf("create isolated network: %w", err)
	}
	networkName := fmt.Sprintf("snaphost-deploy-%s", req.DeployID)

	// 3. Build Traefik labels.
	labels := BuildTraefikLabels(req.DeployID, req.UserID, req.Subdomain, b.cfg.DomainSuffix, req.Port)

	// 4. Build container config.
	exposedPort := nat.Port(fmt.Sprintf("%d/tcp", req.Port))
	envVars := sanitizeEnv(req.Env)

	containerCfg := &container.Config{
		Image:  req.ImageRef,
		Env:    envVars,
		Labels: labels,
		ExposedPorts: nat.PortSet{
			exposedPort: struct{}{},
		},
	}

	// 5. Build host config.
	hostCfg := BuildHostConfig(b.cfg, networkName)

	// 6. Create the container.
	b.publishLog(req.DeployID, "runtime-startup", "creating container", "info")

	resp, err := b.cli.ContainerCreate(ctx, containerCfg, hostCfg, &network.NetworkingConfig{}, &ocispec.Platform{}, containerName)
	if err != nil {
		// Clean up the network on failure.
		_ = DestroyNetwork(ctx, b.cli, networkName)
		return nil, fmt.Errorf("%w: %s", backend.ErrContainerStartFailed, err.Error())
	}

	// 7. Connect to Traefik network for routing.
	if err := ConnectToTraefikNetwork(ctx, b.cli, resp.ID); err != nil {
		// Clean up on failure.
		_ = b.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = DestroyNetwork(ctx, b.cli, networkName)
		return nil, fmt.Errorf("connect to traefik network: %w", err)
	}

	// 8. Start the container.
	if err := b.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = b.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = DestroyNetwork(ctx, b.cli, networkName)
		return nil, fmt.Errorf("%w: %s", backend.ErrContainerStartFailed, err.Error())
	}

	b.publishLog(req.DeployID, "runtime-startup", "container started, waiting for health check", "info")

	// 9. Health wait: poll every 500ms for up to 30 seconds.
	if err := b.waitForHealthy(ctx, resp.ID, req.DeployID, networkName); err != nil {
		return nil, err
	}

	// 10. Build result.
	now := time.Now().UTC()
	endpointURL := fmt.Sprintf("http://%s.%s", req.Subdomain, b.cfg.DomainSuffix)

	b.publishLog(req.DeployID, "runtime-startup", fmt.Sprintf("deployment live at %s", endpointURL), "info")

	return &backend.RunResult{
		ContainerID:  resp.ID,
		EndpointURL:  endpointURL,
		StartedAt:    now,
		TTLExpiresAt: now.Add(req.TTL),
	}, nil
}

// Stop gracefully shuts down the container and cleans up associated resources.
func (b *DockerBackend) Stop(ctx context.Context, deployID, containerID string) error {
	// Inspect to get deploy ID for network cleanup.
	info, err := b.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		// If the container is already gone, nothing to clean up.
		if client.IsErrNotFound(err) {
			b.publishLog(deployID, "runtime-shutdown", "container already absent", "warn")
			b.log.Warn("container already removed during stop", zap.String("container_id", containerID))
			return nil
		}
		return fmt.Errorf("inspect container for stop: %w", err)
	}

	containerDeployID := info.Config.Labels["snaphost.deploy.id"]
	if containerDeployID == "" {
		containerDeployID = deployID
	}

	// Graceful stop with 10 second timeout.
	stopTimeout := 10
	b.publishLog(deployID, "runtime-shutdown", "container delete started", "info")
	b.log.Info("stopping container", zap.String("container_id", containerID), zap.String("deploy_id", containerDeployID))

	if err := b.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &stopTimeout}); err != nil {
		if !client.IsErrNotFound(err) {
			b.log.Warn("container stop returned error", zap.Error(err))
		}
	}

	// Remove the container.
	if err := b.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		if !client.IsErrNotFound(err) {
			b.log.Warn("container remove returned error", zap.Error(err))
		}
	}

	// Remove the isolated network (best effort).
	if containerDeployID != "" {
		networkName := fmt.Sprintf("snaphost-deploy-%s", containerDeployID)
		if err := DestroyNetwork(ctx, b.cli, networkName); err != nil {
			b.log.Warn("failed to remove isolated network", zap.String("network", networkName), zap.Error(err))
		}
	}

	b.publishLog(deployID, "runtime-shutdown", "container delete succeeded", "info")
	b.log.Info("container stopped and cleaned up", zap.String("container_id", containerID))
	return nil
}

// HealthCheck reports whether the deployment container is currently running.
func (b *DockerBackend) HealthCheck(ctx context.Context, containerID string) (*backend.HealthStatus, error) {
	info, err := b.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, fmt.Errorf("%w: %s", backend.ErrContainerNotFound, containerID)
		}
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	startedAt, _ := time.Parse(time.RFC3339Nano, info.State.StartedAt)

	return &backend.HealthStatus{
		Running:     info.State.Running,
		LastStarted: startedAt,
		Message:     info.State.Status,
	}, nil
}

// StreamLogs tails container stdout/stderr. The returned channel emits lines
// until the context is cancelled or the container dies.
func (b *DockerBackend) StreamLogs(ctx context.Context, containerID string) (<-chan string, error) {
	reader, err := b.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: false,
		Tail:       "0",
	})
	if err != nil {
		return nil, fmt.Errorf("open container logs: %w", err)
	}

	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		defer reader.Close()

		// Docker multiplexes stdout/stderr with an 8-byte header per frame.
		// Use stdcopy to demux into a pipe, then scan lines.
		pr, pw := io.Pipe()
		go func() {
			_, _ = stdcopy.StdCopy(pw, pw, reader)
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// Name returns the backend identifier.
func (b *DockerBackend) Name() string {
	return "docker"
}

// waitForHealthy polls ContainerInspect every 500ms for up to 30 seconds,
// requiring the container to be running and up for at least 2 seconds.
func (b *DockerBackend) waitForHealthy(ctx context.Context, containerID, deployID, networkName string) error {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			// Timed out — collect last 100 lines of logs and clean up.
			return b.handleUnhealthy(ctx, containerID, deployID, networkName)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := b.cli.ContainerInspect(ctx, containerID)
			if err != nil {
				continue
			}

			if !info.State.Running {
				// Container crashed — clean up immediately.
				return b.handleUnhealthy(ctx, containerID, deployID, networkName)
			}

			// Quick crash detection: container must have been running for at least 2 seconds.
			startedAt, _ := time.Parse(time.RFC3339Nano, info.State.StartedAt)
			if time.Since(startedAt) >= 2*time.Second {
				return nil // healthy
			}
		}
	}
}

// handleUnhealthy fetches the last 100 lines of logs, publishes them, cleans up,
// and returns ErrHealthCheckTimeout.
func (b *DockerBackend) handleUnhealthy(ctx context.Context, containerID, deployID, networkName string) error {
	// Fetch last 100 lines of logs.
	logReader, err := b.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "100",
	})
	if err == nil {
		scanner := bufio.NewScanner(logReader)
		for scanner.Scan() {
			b.publishLog(deployID, "runtime-startup", scanner.Text(), "error")
		}
		logReader.Close()
	}

	// Clean up.
	_ = b.cli.ContainerStop(ctx, containerID, container.StopOptions{})
	_ = b.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	_ = DestroyNetwork(ctx, b.cli, networkName)

	b.publishLog(deployID, "runtime-startup", "container failed health check — removed", "error")

	return fmt.Errorf("%w: container did not become healthy within 30 seconds", backend.ErrHealthCheckTimeout)
}

// publishLog is a convenience wrapper around the publisher.
func (b *DockerBackend) publishLog(deployID, stage, text, level string) {
	if b.publisher == nil {
		return
	}
	_ = b.publisher.Publish(deployID, logs.LogLine{
		Stage: stage,
		Text:  text,
		Level: level,
	})
}

// sanitizeEnv converts a map to []string{"KEY=VAL"} format, stripping any
// env var whose name starts with SNAPHOST_ (reserved namespace) or contains
// DOCKER_HOST (security boundary).
func sanitizeEnv(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for k, v := range env {
		if strings.HasPrefix(k, "SNAPHOST_") {
			continue
		}
		if strings.Contains(k, "DOCKER_HOST") {
			continue
		}
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
