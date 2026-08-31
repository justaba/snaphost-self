package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
	"go.uber.org/zap"

	"snaphost/internal/runtime/config"
)

// Tearing a deploy down has produced three leaks, each a Docker call that was
// swallowed or skipped: a container removal that logged and returned nil, a
// network removal that did the same, and a network never attempted at all
// because an absent container returned early. Every one of them ended with a
// resource on the host that no sweep could find, because the rows naming it
// were deleted immediately afterwards.
//
// None of them were testable until the client sat behind an interface.

type fakeTeardown struct {
	inspectErr error
	stopErr    error
	removeErr  error
	networkErr error

	calls    []string
	networks []string
}

func (f *fakeTeardown) ContainerInspect(_ context.Context, containerID string) (types.ContainerJSON, error) {
	f.calls = append(f.calls, "inspect:"+containerID)
	if f.inspectErr != nil {
		return types.ContainerJSON{}, f.inspectErr
	}
	return types.ContainerJSON{
		Config: &container.Config{Labels: map[string]string{"snaphost.deploy.id": "labelled-id"}},
	}, nil
}

func (f *fakeTeardown) ContainerStop(_ context.Context, containerID string, _ container.StopOptions) error {
	f.calls = append(f.calls, "stop:"+containerID)
	return f.stopErr
}

func (f *fakeTeardown) ContainerRemove(_ context.Context, containerID string, _ container.RemoveOptions) error {
	f.calls = append(f.calls, "remove:"+containerID)
	return f.removeErr
}

func (f *fakeTeardown) NetworkRemove(_ context.Context, networkID string) error {
	f.calls = append(f.calls, "network:"+networkID)
	f.networks = append(f.networks, networkID)
	return f.networkErr
}

func newTeardownBackend(fake *fakeTeardown) *DockerBackend {
	return &DockerBackend{teardown: fake, cfg: &config.Config{}, log: zap.NewNop()}
}

func notFound() error { return errdefs.NotFound(errors.New("no such container")) }

// The regression this file was written for. A first attempt that removed the
// container and then failed on the network leaves the container absent; the
// retry used to see not-found and return success without touching the network,
// so the second attempt was the one that deleted the rows and stranded it.
func TestStopRemovesTheNetworkWhenTheContainerIsAlreadyGone(t *testing.T) {
	fake := &fakeTeardown{inspectErr: notFound()}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(fake.networks) != 1 || fake.networks[0] != "snaphost-deploy-deploy-1" {
		t.Fatalf("networks removed = %v, want the deploy's own; a retry after a partial cleanup would strand it", fake.networks)
	}
}

// And it must report a network it could not remove, even on that path —
// otherwise the caller deletes the rows believing the host is clean.
func TestStopReportsANetworkFailureOnTheAlreadyGonePath(t *testing.T) {
	fake := &fakeTeardown{inspectErr: notFound(), networkErr: errors.New("network has active endpoints")}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err == nil {
		t.Fatal("Stop reported success while the network was still on the host")
	}
}

func TestStopRemovesContainerThenNetwork(t *testing.T) {
	fake := &fakeTeardown{}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{"inspect:container-1", "stop:container-1", "remove:container-1", "network:snaphost-deploy-labelled-id"}
	if len(fake.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
	for i := range want {
		if fake.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", fake.calls, want)
		}
	}
}

// The label wins when it is readable: it is what the container was actually
// created with, and the argument is only the caller's belief about it.
func TestStopPrefersTheContainerLabelForTheNetworkName(t *testing.T) {
	fake := &fakeTeardown{}

	if err := newTeardownBackend(fake).Stop(context.Background(), "argument-id", "container-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if fake.networks[0] != "snaphost-deploy-labelled-id" {
		t.Errorf("network = %q, want the labelled deploy id", fake.networks[0])
	}
}

// A forced removal follows, and it is the removal that decides whether the
// container is gone.
func TestStopContinuesPastAFailedGracefulStop(t *testing.T) {
	fake := &fakeTeardown{stopErr: errors.New("cannot stop")}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(fake.networks) != 1 {
		t.Error("a failed graceful stop skipped the rest of the teardown")
	}
}

// Callers act on this answer: the deploy is recorded stopped, and project
// deletion hard-deletes the rows naming the container right afterwards.
func TestStopReportsAFailedContainerRemoval(t *testing.T) {
	fake := &fakeTeardown{removeErr: errors.New("device or resource busy")}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err == nil {
		t.Fatal("Stop reported success for a container still on the host")
	}
	if len(fake.networks) != 0 {
		t.Error("the network was removed after the container removal failed; the deploy is half torn down")
	}
}

// Not-found on the removal is the outcome the caller wanted, and compensation
// re-runs this path, so it has to stay a success.
func TestStopTreatsAnAbsentContainerRemovalAsDone(t *testing.T) {
	fake := &fakeTeardown{removeErr: notFound()}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(fake.networks) != 1 {
		t.Error("the network was not removed")
	}
}

// An inspect failure that is not not-found is a daemon this code cannot reason
// about. Guessing is what produced the leaks above.
func TestStopRefusesWhenTheDaemonCannotBeQueried(t *testing.T) {
	fake := &fakeTeardown{inspectErr: errors.New("connection refused")}

	if err := newTeardownBackend(fake).Stop(context.Background(), "deploy-1", "container-1"); err == nil {
		t.Fatal("Stop reported success without knowing the container's state")
	}
	if len(fake.calls) != 1 {
		t.Errorf("calls = %v, want the inspect only", fake.calls)
	}
}

// DestroyNetwork swallows not-found so every re-run of the teardown succeeds.
func TestDestroyNetworkTreatsAMissingNetworkAsDone(t *testing.T) {
	fake := &fakeTeardown{networkErr: notFound()}

	if err := DestroyNetwork(context.Background(), fake, "snaphost-deploy-1"); err != nil {
		t.Fatalf("DestroyNetwork: %v", err)
	}
}
