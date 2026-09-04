package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"snaphost/internal/runtime/config"
)

// CreateIsolatedNetwork creates a Docker bridge network for a single deployment.
// Inter-container communication (ICC) is disabled so user containers on the
// same network cannot communicate with each other.
func CreateIsolatedNetwork(ctx context.Context, cli *client.Client, deployID string) (string, error) {
	name := fmt.Sprintf("snaphost-deploy-%s", deployID)

	resp, err := cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Options: map[string]string{
			"com.docker.network.bridge.enable_icc":           "false",
			"com.docker.network.bridge.enable_ip_masquerade": "true",
		},
	})
	if err != nil {
		return "", fmt.Errorf("create network %s: %w", name, err)
	}

	return resp.ID, nil
}

// DestroyNetwork removes a Docker network by name. Best-effort — errors are
// returned so callers can decide whether to log-and-continue or fail.
// A network that is not there is the outcome the caller wanted. Compensation,
// the watchdog and project deletion all re-run this path, and a deploy whose
// network was never created — or was removed by an earlier attempt — must not
// look like a failure.
func DestroyNetwork(ctx context.Context, cli NetworkRemover, networkName string) error {
	if err := cli.NetworkRemove(ctx, networkName); err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove network %s: %w", networkName, err)
	}
	return nil
}

// ConnectToRoutingNetwork attaches a container to the shared routing network
// so the control plane and either supported edge path can reach it.
func ConnectToRoutingNetwork(ctx context.Context, cli *client.Client, containerID string) error {
	if err := cli.NetworkConnect(ctx, config.RoutingNetwork, containerID, nil); err != nil {
		return fmt.Errorf("connect container %s to %s: %w", containerID, config.RoutingNetwork, err)
	}
	return nil
}

// NetworkRemover is the one method DestroyNetwork needs. Taking an interface
// rather than *client.Client is what lets the teardown path be tested.
type NetworkRemover interface {
	NetworkRemove(ctx context.Context, networkID string) error
}
