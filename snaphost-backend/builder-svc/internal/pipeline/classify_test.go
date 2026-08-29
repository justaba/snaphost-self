package pipeline

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"snaphost/builder-svc/internal/ai"
	"snaphost/builder-svc/internal/clone"
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
	t.Run("ErrAIUnavailable → transient", func(t *testing.T) {
		got := classifyAIError(fmt.Errorf("gen: %w", ai.ErrAIUnavailable), log)
		if !IsTransient(got) {
			t.Errorf("want transient, got %v", got)
		}
	})
	t.Run("ErrAIRefused (422) → permanent", func(t *testing.T) {
		got := classifyAIError(fmt.Errorf("gen: %w: cannot generate", ai.ErrAIRefused), log)
		if !IsPermanent(got) {
			t.Errorf("want permanent, got %v", got)
		}
	})
	t.Run("ErrAIRefused 400 → permanent (with WARN log)", func(t *testing.T) {
		// We don't assert on log output here (NewNop discards) — just
		// verify classification. WARN emission is visual at runtime.
		got := classifyAIError(fmt.Errorf("gen: %w: status 400: bad request", ai.ErrAIRefused), log)
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
