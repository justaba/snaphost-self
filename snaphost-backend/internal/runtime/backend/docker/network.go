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
func DestroyNetwork(ctx context.Context, cli *client.Client, networkName string) error {
	if err := cli.NetworkRemove(ctx, networkName); err != nil {
		return fmt.Errorf("remove network %s: %w", networkName, err)
	}
	return nil
}

// ConnectToTraefikNetwork attaches a container to the shared Traefik network so
// that Traefik can route traffic to it. In dev this is a local bridge network;
// in prod with the Yandex backend this function is not called (cloud-native routing).
func ConnectToTraefikNetwork(ctx context.Context, cli *client.Client, containerID string) error {
	if err := cli.NetworkConnect(ctx, config.TraefikNetwork, containerID, nil); err != nil {
		return fmt.Errorf("connect container %s to %s: %w", containerID, config.TraefikNetwork, err)
	}
	return nil
}
