package scan

import (
	"slices"
	"testing"
)

// The scan reads the image from the local Docker daemon, because that is where
// the build put it. Asking Trivy for "remote" would send it to a registry for
// something that was never pushed — and on the local stack that registry name
// does not even resolve from the daemon's side of the network.
func TestScanReadsTheImageFromTheLocalDaemon(t *testing.T) {
	args := (&Scanner{}).buildArgs("snaphost/proj-abcd1234:deploy-1")

	src := flagValue(args, "--image-src")
	if src != "docker" {
		t.Fatalf("--image-src = %q, want docker", src)
	}
	if slices.Contains(args, "--insecure") {
		t.Error("--insecure is present; there is no TLS to skip when nothing is fetched")
	}
}

// The image reference has to be last: Trivy takes it as a positional argument,
// and a flag appended after it is read as a second target.
func TestTheImageReferenceIsTheFinalArgument(t *testing.T) {
	const ref = "snaphost/proj-abcd1234:deploy-1"
	args := (&Scanner{}).buildArgs(ref)

	if len(args) == 0 || args[len(args)-1] != ref {
		t.Fatalf("last argument = %q, want %q", args[len(args)-1], ref)
	}
	if args[0] != "image" {
		t.Fatalf("first argument = %q, want the image subcommand", args[0])
	}
}

// Severity and exit code are the two settings that decide what a scan means:
// the pipeline reads the report and applies its own policy, so Trivy must not
// fail the process on a finding.
func TestScanReportsRatherThanFails(t *testing.T) {
	args := (&Scanner{}).buildArgs("snaphost/proj-abcd1234:deploy-1")

	if got := flagValue(args, "--exit-code"); got != "0" {
		t.Errorf("--exit-code = %q, want 0", got)
	}
	if got := flagValue(args, "--severity"); got != "CRITICAL,HIGH" {
		t.Errorf("--severity = %q", got)
	}
	if got := flagValue(args, "--format"); got != "json" {
		t.Errorf("--format = %q, want json", got)
	}
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
