package docker

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// teardownClient is the slice of the Docker API that tearing a deploy down
// uses. It exists so Stop can be tested.
//
// Without it the only assertion available was "the caller handles a refusal",
// which is not the same as "the refusal happens" — and this path has produced
// three separate leaks, each of them a swallowed or skipped Docker call:
// a container removal that logged and returned nil, a network removal that did
// the same, and a network that was never attempted at all because an absent
// container returned early.
//
// *client.Client satisfies it. Nothing else in this package changes shape.
type teardownClient interface {
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	NetworkRemove(ctx context.Context, networkID string) error
}
