package pipeline

import (
	"strings"
	"testing"
)

func TestImageRefForJob(t *testing.T) {
	const (
		userID   = "a4a355f8-9769-454f-b5c0-9782acceebc0"
		deployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"
	)

	got := imageRefForJob(userID, deployID)

	if !strings.HasPrefix(got, "snaphost/proj-") {
		t.Fatalf("imageRefForJob() = %q, want the snaphost/proj- namespace", got)
	}
	if !strings.HasSuffix(got, ":"+deployID) {
		t.Fatalf("imageRefForJob() = %q, want the deploy id as the tag", got)
	}

	// The account id must not reach the image name. It ends up in `docker ps`
	// output and in build logs, and neither needs it.
	if strings.Contains(got, userID) {
		t.Fatalf("imageRefForJob() = %q leaks the user id", got)
	}

	// A registry host would send the runtime looking for something to pull.
	// The image is built into the local daemon and never leaves it.
	if strings.Contains(got, ":5000") || strings.Contains(got, "cr.yandex") {
		t.Fatalf("imageRefForJob() = %q names a registry", got)
	}
}

// Two deploys by the same account share a namespace; two accounts must not.
func TestImageRefSeparatesAccounts(t *testing.T) {
	const deployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"

	first := imageRefForJob("a4a355f8-9769-454f-b5c0-9782acceebc0", deployID)
	second := imageRefForJob("11111111-2222-4333-8444-555555555555", deployID)

	if first == second {
		t.Fatalf("two accounts produced the same image name: %q", first)
	}
}
