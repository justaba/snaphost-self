package docker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/docker/docker/api/types/network"

	"snaphost/internal/runtime/backend"
	"snaphost/internal/runtime/config"
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

// containerAddress returns the address the probe should dial.
//
// A deploy container joins two networks: its own isolated one, created per
// deploy with inter-container communication disabled, and the shared network
// this process is also on. Only the second is reachable from here — the first
// exists precisely so that nothing else can reach it.
//
// This used to return the first address the map produced, with a comment
// claiming either would do. Go randomises map iteration order, so the probe
// dialled the unreachable address about half the time and the deploy failed
// with "nothing answered on the port", blaming the user's application for a
// coin flip. The shared network is named, and the fallback is last.
func containerAddress(networks map[string]*network.EndpointSettings) string {
	if endpoint := networks[config.TraefikNetwork]; endpoint != nil && endpoint.IPAddress != "" {
		return endpoint.IPAddress
	}

	// Nothing on the shared network. Any address is a better answer than none:
	// a backend that does not attach to it still has to be probed somehow.
	for _, name := range sortedNames(networks) {
		if endpoint := networks[name]; endpoint != nil && endpoint.IPAddress != "" {
			return endpoint.IPAddress
		}
	}
	return ""
}

// sortedNames keeps the fallback deterministic, so a probe failure is
// reproducible rather than intermittent.
func sortedNames(networks map[string]*network.EndpointSettings) []string {
	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
