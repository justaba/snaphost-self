// Package docker implements the backend.Backend interface using the local Docker
// daemon. All Docker SDK calls are isolated in this package — nothing outside
// internal/backend/docker/ may import the Docker SDK.
package docker

import (
	"bufio"
	"bytes"
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

// Run starts the image on an isolated network, connects it to the Traefik
// network, waits for it to be healthy, and returns the public endpoint URL.
func (b *DockerBackend) Run(ctx context.Context, req backend.RunRequest) (*backend.RunResult, error) {
	// Apply overall timeout.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	containerName := fmt.Sprintf("snaphost-deploy-%s", req.DeployID)

	// 1. The image is already here — the build loaded it into this daemon.
	//
	// This used to be a pull, which is what a registry was for. On one host the
	// image never leaves the machine it was built on, so a pull was a network
	// round trip to fetch something already on disk. It also could not work:
	// the builder pushed to a name resolvable only inside the Docker network,
	// and this call is made by the host daemon, which is not on it.
	//
	// Absence is a real failure rather than a reason to fetch. A deploy whose
	// image is missing is one whose build did not produce what it said it did,
	// and starting a pull would turn that into a confusing registry error.
	if _, _, err := b.cli.ImageInspectWithRaw(ctx, req.ImageRef); err != nil {
		return nil, fmt.Errorf("%w: image %s is not in the local store: %s",
			backend.ErrImagePullFailed, req.ImageRef, err.Error())
	}

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
			return b.handleUnhealthy(ctx, containerID, deployID, networkName,
				"did not stay up for 30 seconds")
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := b.cli.ContainerInspect(ctx, containerID)
			if err != nil {
				continue
			}

			if !info.State.Running {
				// Container crashed — clean up immediately. The reason names the
				// exit rather than the deadline: reporting a 30-second timeout
				// for a container that died in under a second sends whoever reads
				// it looking for something slow, when the answer is in the last
				// lines of its log.
				return b.handleUnhealthy(ctx, containerID, deployID, networkName,
					fmt.Sprintf("exited with code %d after %s", info.State.ExitCode, sinceStart(info.State.StartedAt)))
			}

			// Quick crash detection: container must have been running for at least 2 seconds.
			startedAt, _ := time.Parse(time.RFC3339Nano, info.State.StartedAt)
			if time.Since(startedAt) >= 2*time.Second {
				return nil // healthy
			}
		}
	}
}

// handleUnhealthy fetches the last 100 lines of logs, publishes them, cleans
// up, and returns ErrHealthCheckTimeout with the reason the caller observed.
func (b *DockerBackend) handleUnhealthy(ctx context.Context, containerID, deployID, networkName, reason string) error {
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

	return fmt.Errorf("%w: container %s", backend.ErrHealthCheckTimeout, reason)
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

// LoadImage reads a Docker image tarball into the daemon's own image store.
//
// It exists because the build pipeline has no way to reach the daemon itself:
// this package is the only one allowed to import the Docker SDK, which is why
// the builder takes an interface and gets this.
//
// The pull it replaces went through a registry, and on a single host that was
// two network hops and a daemon to move an image between two processes that
// share a filesystem. It also could not work locally: the builder pushed to a
// Compose service name resolvable only inside the Docker network, and the pull
// was performed by the host daemon, which is not on that network.
func (b *DockerBackend) LoadImage(ctx context.Context, r io.Reader) error {
	// quiet=true: the progress stream is for a terminal, and this one is read
	// only to find out whether the load failed.
	resp, err := b.cli.ImageLoad(ctx, r, true)
	if err != nil {
		return fmt.Errorf("load image into the docker daemon: %w", err)
	}
	defer resp.Body.Close()

	// The response is a progress stream. Draining it is what makes the load
	// actually happen — the same reason the pull it replaced was drained.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read image load response: %w", err)
	}
	// The daemon reports a failed load in the body with a 200 status, so the
	// only way to notice is to look.
	if bytes.Contains(body, []byte(`"errorDetail"`)) {
		return fmt.Errorf("docker rejected the image: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

// RemoveImage deletes an image from the daemon's store.
//
// The build pipeline calls it when a scan finds critical vulnerabilities and
// the gate is on, so a rejected artifact does not sit on the host. It used to
// be a DELETE against the registry's v2 API; the image never leaves this daemon
// now, so this is where it has to be removed from.
//
// force=true because the image was tagged by the build and nothing else refers
// to it; prune untagged parents, since the layers under a rejected image are
// not wanted either.
func (b *DockerBackend) RemoveImage(ctx context.Context, imageRef string) error {
	_, err := b.cli.ImageRemove(ctx, imageRef, image.RemoveOptions{Force: true, PruneChildren: true})
	if err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove image %s: %w", imageRef, err)
	}
	return nil
}

// sinceStart reports how long a container ran, for the failure message. An
// unparseable start time yields "an unknown time" rather than a wrong number.
func sinceStart(startedAt string) string {
	t, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return "an unknown time"
	}
	return time.Since(t).Round(time.Millisecond).String()
}
