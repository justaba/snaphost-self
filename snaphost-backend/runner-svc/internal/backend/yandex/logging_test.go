package yandex

import (
	"errors"
	"testing"

	loggingpb "github.com/yandex-cloud/go-genproto/yandex/cloud/logging/v1"
	containerspb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/containers/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRevisionLogOptionsUsesConfiguredGroup(t *testing.T) {
	opts := revisionLogOptions("grp-1", "", false)

	if opts.GetDisabled() {
		t.Fatal("collection must be on when a group is configured")
	}
	dest, ok := opts.GetDestination().(*containerspb.LogOptions_LogGroupId)
	if !ok {
		t.Fatalf("expected a log group destination, got %T", opts.GetDestination())
	}
	if dest.LogGroupId != "grp-1" {
		t.Fatalf("log group = %q, want grp-1", dest.LogGroupId)
	}
}

// Sending no options is not the same as disabling: Yandex then applies its own
// default and writes to the folder's default group. Naming the folder
// explicitly would do the same thing while adding a permission check that can
// fail, which is what took production deploys down on 2026-08-04.
func TestRevisionLogOptionsUnconfiguredSendsNothing(t *testing.T) {
	if opts := revisionLogOptions("", "", false); opts != nil {
		t.Fatalf("expected no log options, got %+v", opts)
	}
}

func TestRevisionLogOptionsDisabledIsExplicit(t *testing.T) {
	opts := revisionLogOptions("grp-1", "INFO", true)

	if !opts.GetDisabled() {
		t.Fatal("expected collection to be disabled")
	}
	if opts.GetDestination() != nil {
		t.Fatal("a disabled request must not also name a destination")
	}
}

func TestRevisionLogOptionsMinLevel(t *testing.T) {
	for name, want := range map[string]loggingpb.LogLevel_Level{
		"TRACE": loggingpb.LogLevel_TRACE,
		"DEBUG": loggingpb.LogLevel_DEBUG,
		"INFO":  loggingpb.LogLevel_INFO,
		"WARN":  loggingpb.LogLevel_WARN,
		"ERROR": loggingpb.LogLevel_ERROR,
		"FATAL": loggingpb.LogLevel_FATAL,
	} {
		if got := revisionLogOptions("grp-1", name, false).GetMinLevel(); got != want {
			t.Errorf("min level for %s = %v, want %v", name, got, want)
		}
	}
}

// An unrecognised level must not silently become a restrictive one: that would
// drop exactly the output an operator is trying to read.
func TestRevisionLogOptionsUnknownMinLevelKeepsProviderDefault(t *testing.T) {
	for _, name := range []string{"", "verbose", "WARNING", "nonsense"} {
		opts := revisionLogOptions("grp-1", name, false)
		if opts.GetMinLevel() != loggingpb.LogLevel_LEVEL_UNSPECIFIED {
			t.Errorf("min level for %q = %v, want unspecified", name, opts.GetMinLevel())
		}
		if opts.GetDisabled() {
			t.Errorf("an unusable level for %q must not disable collection", name)
		}
	}
}

// The exact error production returned on 2026-08-04, plus its neighbours.
// Recognising it is what lets the deploy proceed without logs instead of
// failing outright.
func TestIsLogOptionsRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"observed in production", status.Error(codes.PermissionDenied, "Not enough permissions to use log group e232536al99cst2cpqph"), true},
		{"group deleted", status.Error(codes.NotFound, "log group not found"), true},
		{"malformed group id", status.Error(codes.InvalidArgument, "invalid log group id"), true},
		{"case insensitive", status.Error(codes.PermissionDenied, "cannot use LOG GROUP x"), true},

		{"unrelated permission failure", status.Error(codes.PermissionDenied, "permission denied for container registry"), false},
		{"image pull failure", status.Error(codes.InvalidArgument, "image not found"), false},
		{"quota", status.Error(codes.ResourceExhausted, "log group quota exceeded"), false},
		{"plain error", errors.New("log group something"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isLogOptionsRejection(tc.err); got != tc.want {
			t.Errorf("%s: isLogOptionsRejection = %v, want %v", tc.name, got, tc.want)
		}
	}
}
