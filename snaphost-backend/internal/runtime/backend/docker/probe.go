package docker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/docker/docker/api/types/network"

	"snaphost/internal/runtime/backend"
)

// probeInterval is how often the probe retries while waiting for the server
// inside the container to bind its port.
const probeInterval = 500 * time.Millisecond

// Probe asks the container directly, on the network address Docker gave it,
// whether anything is listening on the port we told the application to use.
//
// It deliberately does not go through Traefik: routing has its own failure
// modes, and the question here is only "did the user's server bind the port
// the runtime injected". Any HTTP response counts as an answer, including a
// 500 — an application that returns errors is still listening, and judging its
// content is not the runtime's business.
func (b *DockerBackend) Probe(ctx context.Context, req backend.ProbeRequest) error {
	info, err := b.cli.ContainerInspect(ctx, req.ContainerID)
	if err != nil {
		return fmt.Errorf("inspect container for probe: %w", err)
	}

	address := containerAddress(info.NetworkSettings.Networks)
	if address == "" {
		// No address means we cannot ask the question; do not fail a deploy
		// over the probe's own blind spot.
		b.publishLog(req.DeployID, "runtime-startup", "probe skipped: container has no network address", "warn")
		return nil
	}

	target := fmt.Sprintf("http://%s", net.JoinHostPort(address, fmt.Sprintf("%d", req.Port)))
	client := &http.Client{Timeout: 3 * time.Second}

	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return fmt.Errorf("build probe request: %w", err)
		}
		resp, err := client.Do(httpReq)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w on port %d: %v", backend.ErrProbeFailed, req.Port, lastErr)
		case <-ticker.C:
		}
	}
}

// containerAddress picks the first usable IPv4 address across the container's
// networks. A deploy container joins its own isolated network plus the Traefik
// one, and either is reachable from runner-svc.
func containerAddress(networks map[string]*network.EndpointSettings) string {
	for _, endpoint := range networks {
		if endpoint == nil {
			continue
		}
		if endpoint.IPAddress != "" {
			return endpoint.IPAddress
		}
	}
	return ""
}
