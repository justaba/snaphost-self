package pipeline

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"snaphost/internal/ai/llm"
	"snaphost/internal/builder/clone"
)

func TestClassifyValidationError(t *testing.T) {
	cases := []struct {
		name      string
		in        error
		transient bool
		permanent bool
	}{
		{"nil → nil", nil, false, false},
		{"ErrResolverFailure → transient", fmt.Errorf("wrap: %w", clone.ErrResolverFailure), true, false},
		{"ErrIPFiltered → permanent", fmt.Errorf("wrap: %w", clone.ErrIPFiltered), false, true},
		{"ErrInvalidURL → permanent", fmt.Errorf("wrap: %w", clone.ErrInvalidURL), false, true},
		{"ErrHostNotAllowed → permanent", fmt.Errorf("wrap: %w", clone.ErrHostNotAllowed), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyValidationError(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("nil → %v", got)
				}
				return
			}
			if IsTransient(got) != tc.transient {
				t.Errorf("IsTransient: got %v want %v (err=%v)", IsTransient(got), tc.transient, got)
			}
			if IsPermanent(got) != tc.permanent {
				t.Errorf("IsPermanent: got %v want %v (err=%v)", IsPermanent(got), tc.permanent, got)
			}
		})
	}
}

func TestClassifyGitError(t *testing.T) {
	t.Run("ErrRepositoryNotFound → permanent", func(t *testing.T) {
		got := classifyGitError(fmt.Errorf("clone: %w", transport.ErrRepositoryNotFound))
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("ErrAuthenticationRequired → permanent", func(t *testing.T) {
		got := classifyGitError(fmt.Errorf("clone: %w", transport.ErrAuthenticationRequired))
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("unexpected host (pinned dialer) → permanent", func(t *testing.T) {
		got := classifyGitError(fmt.Errorf("clone: dial to unexpected host %q", "attacker.example.com"))
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("net.OpError → transient", func(t *testing.T) {
		opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		got := classifyGitError(fmt.Errorf("clone: %w", opErr))
		if !IsTransient(got) {
			t.Errorf("want transient, got %v", got)
		}
	})
	t.Run("unknown error → permanent (conservative default)", func(t *testing.T) {
		got := classifyGitError(errors.New("some random opaque go-git error"))
		if !IsPermanent(got) {
			t.Errorf("want permanent (default), got %v", got)
		}
	})
	t.Run("nil → nil", func(t *testing.T) {
		if got := classifyGitError(nil); got != nil {
			t.Errorf("nil → %v", got)
		}
	})
}

func TestClassifyAIError(t *testing.T) {
	log := zap.NewNop()
	for _, sentinel := range []error{llm.ErrTimeout, llm.ErrRateLimited, llm.ErrCircuitOpen, llm.ErrUpstream} {
		got := classifyAIError(fmt.Errorf("gen: %w", sentinel), log)
		if !IsTransient(got) {
			t.Errorf("%v: want transient, got %v", sentinel, got)
		}
	}
	t.Run("invalid model output → permanent", func(t *testing.T) {
		got := classifyAIError(fmt.Errorf("gen: %w", llm.ErrInvalidOutput), log)
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("unknown error → permanent", func(t *testing.T) {
		got := classifyAIError(errors.New("totally unknown"), log)
		if !IsPermanent(got) {
			t.Errorf("want permanent (default), got %v", got)
		}
	})
	t.Run("nil → nil", func(t *testing.T) {
		if got := classifyAIError(nil, log); got != nil {
			t.Errorf("nil → %v", got)
		}
	})
}

func TestClassifyBuildKitError(t *testing.T) {
	t.Run("codes.Unavailable → transient", func(t *testing.T) {
		err := status.Error(codes.Unavailable, "buildkitd unreachable")
		got := classifyBuildKitError(fmt.Errorf("build: %w", err))
		if !IsTransient(got) {
			t.Errorf("want transient, got %v", got)
		}
	})
	t.Run("codes.InvalidArgument → permanent", func(t *testing.T) {
		err := status.Error(codes.InvalidArgument, "bad Dockerfile syntax")
		got := classifyBuildKitError(fmt.Errorf("build: %w", err))
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("plain error (no grpc status) → permanent", func(t *testing.T) {
		got := classifyBuildKitError(errors.New("RUN exited with code 1"))
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("nil → nil", func(t *testing.T) {
		if got := classifyBuildKitError(nil); got != nil {
			t.Errorf("nil → %v", got)
		}
	})
}

// What an operator saw when a RUN step failed was three hundred characters of
// generated shell followed by "exit code: 1" — the entire if/elif chain that
// picks a package manager, and not one word about what went wrong. The reason
// was in the step's output, which is streamed to the build log and archived
// with the deploy, so the message was long, useless, and pointing nowhere.
func TestAFailedBuildStepIsSummarised(t *testing.T) {
	raw := errors.New(`build: buildkit solve: failed to solve: process "/bin/sh -c if [ -f pnpm-lock.yaml ]; ` +
		`then npm install -g pnpm && pnpm install --frozen-lockfile;   elif [ -f yarn.lock ]; ` +
		`then yarn install --frozen-lockfile;   else npm ci;   fi" did not complete successfully: exit code: 1`)

	got := summariseSolveError(raw).Error()

	if strings.Contains(got, "pnpm-lock.yaml") {
		t.Fatalf("the shell command survived into the message: %q", got)
	}
	if !strings.Contains(got, "exited with code 1") {
		t.Fatalf("the exit code was dropped; it is the one fact the original carried: %q", got)
	}
	if !strings.Contains(got, "build log") {
		t.Fatalf("the message does not say where the output is: %q", got)
	}
}

// BuildKit's other failures name their own problem, so rewriting them would
// lose information rather than add it.
func TestOtherSolveErrorsSurviveUnchanged(t *testing.T) {
	cases := []string{
		"build: buildkit solve: failed to solve: dockerfile parse error on line 4: unknown instruction: RUNN",
		"build: buildkit solve: failed to solve: failed to resolve source metadata for docker.io/library/nosuchimage:latest",
	}
	for _, msg := range cases {
		if got := summariseSolveError(errors.New(msg)).Error(); got != msg {
			t.Errorf("summariseSolveError rewrote a message it should not have:\n got %q\nwant %q", got, msg)
		}
	}
	if summariseSolveError(nil) != nil {
		t.Error("summariseSolveError(nil) is not nil")
	}
}
