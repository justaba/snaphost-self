package docker

import "testing"

func TestBuildTraefikLabelsIncludesSnapHostOwnership(t *testing.T) {
	labels := BuildTraefikLabels(
		"b132cb0d-ce92-4009-a8f3-221d86d8607c",
		"a4a355f8-9769-454f-b5c0-9782acceebc0",
		"proj-b132",
		"localhost",
		3000,
	)

	if labels["snaphost.deploy.managed_by"] != "snaphost" {
		t.Fatalf("managed_by = %q, want snaphost", labels["snaphost.deploy.managed_by"])
	}
	if labels["snaphost.deploy.id"] != "b132cb0d-ce92-4009-a8f3-221d86d8607c" {
		t.Fatalf("deploy id label = %q", labels["snaphost.deploy.id"])
	}
	if labels["snaphost.deploy.user_id"] != "a4a355f8-9769-454f-b5c0-9782acceebc0" {
		t.Fatalf("user id label = %q", labels["snaphost.deploy.user_id"])
	}
}

func TestBuildTraefikLabelsOmitsRouterInProduction(t *testing.T) {
	labels := BuildTraefikLabels("deploy", "user", "internal-slug", "", 3000)
	if labels["snaphost.deploy.managed_by"] != "snaphost" {
		t.Fatalf("ownership labels missing: %#v", labels)
	}
	for key := range labels {
		if len(key) >= len("traefik.") && key[:len("traefik.")] == "traefik." {
			t.Fatalf("production label set contains %q", key)
		}
	}
}
