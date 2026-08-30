package docker

import (
	"testing"

	"github.com/docker/docker/api/types/network"

	"snaphost/internal/runtime/config"
)

// A deploy container sits on two networks: its own, created per deploy with
// inter-container communication disabled, and the shared one this process is
// also attached to. Only the shared one is reachable from here — the isolated
// network exists so that nothing else can reach it.
//
// containerAddress used to return whichever address the map happened to yield
// first. Go randomises map iteration, so the probe dialled the unreachable
// address about half the time and the deploy failed with "nothing answered on
// the port", which blames the user's application for a coin flip.
//
// The loop below runs the lookup many times because a single call passes with
// the old code roughly half the time. Anything order-dependent shows up here.
func TestProbeDialsTheSharedNetwork(t *testing.T) {
	networks := map[string]*network.EndpointSettings{
		"snaphost-deploy-096abf84": {IPAddress: "172.21.0.2"},
		config.TraefikNetwork:      {IPAddress: "172.20.0.7"},
	}

	for i := 0; i < 200; i++ {
		if got := containerAddress(networks); got != "172.20.0.7" {
			t.Fatalf("containerAddress() = %q on iteration %d, want the shared-network address 172.20.0.7", got, i)
		}
	}
}

// A backend that never attaches to the shared network still has to be probed,
// and the answer must be the same one every time so that a failure can be
// reproduced instead of retried until it looks fine.
func TestProbeFallbackIsDeterministic(t *testing.T) {
	networks := map[string]*network.EndpointSettings{
		"zeta":  {IPAddress: "172.30.0.4"},
		"alpha": {IPAddress: "172.29.0.9"},
	}

	first := containerAddress(networks)
	if first == "" {
		t.Fatal("containerAddress() = \"\" with two usable addresses")
	}
	for i := 0; i < 200; i++ {
		if got := containerAddress(networks); got != first {
			t.Fatalf("containerAddress() returned %q then %q; the fallback is order-dependent", first, got)
		}
	}
}

func TestProbeSkipsEndpointsWithNoAddress(t *testing.T) {
	networks := map[string]*network.EndpointSettings{
		config.TraefikNetwork: {IPAddress: ""},
		"snaphost-deploy-x":   nil,
		"late-attach":         {IPAddress: "172.31.0.3"},
	}

	if got := containerAddress(networks); got != "172.31.0.3" {
		t.Fatalf("containerAddress() = %q, want the only usable address", got)
	}
	if got := containerAddress(nil); got != "" {
		t.Fatalf("containerAddress(nil) = %q, want the empty string", got)
	}
}
