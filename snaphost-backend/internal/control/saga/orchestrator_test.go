package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestOperationErrorPermanence(t *testing.T) {
	cases := []struct {
		name          string
		retryable     bool
		wantPermanent bool
	}{
		{"validation failure", false, true},
		{"temporary runtime failure", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &OperationError{Retryable: tc.retryable}
			if got := err.Permanent(); got != tc.wantPermanent {
				t.Fatalf("Permanent() = %v, want %v", got, tc.wantPermanent)
			}
		})
	}
}

func TestOperationErrorUserReasonPrefersMessage(t *testing.T) {
	withMessage := &OperationError{
		Code:    "probe_failed",
		Message: "the container started but nothing answered on the port the runtime injects.",
	}
	if withMessage.UserReason() != withMessage.Message {
		t.Fatalf("UserReason() = %q, want the downstream message verbatim", withMessage.UserReason())
	}

	bare := &OperationError{Code: "probe_failed"}
	if bare.UserReason() == "" {
		t.Fatal("a reason is shown to the deploy owner; it must never be empty")
	}
}

func TestOperationErrorIsMatchable(t *testing.T) {
	var target *OperationError
	wrapped := errors.Join(errors.New("runtime deploy"), &OperationError{Code: "probe_failed"})
	if !errors.As(wrapped, &target) {
		t.Fatal("the orchestrator matches with errors.As; a wrapped OperationError must still be found")
	}
}

type rejectingBuildScheduler struct{ err error }

func (s rejectingBuildScheduler) EnqueueBuild(context.Context, BuildRequest) error {
	return s.err
}

func TestEnqueueBuildClassifiesOperationErrors(t *testing.T) {
	tests := []struct {
		name         string
		retryable    bool
		wantTerminal bool
	}{
		{"invalid request is terminal", false, true},
		{"temporary pressure is retryable", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Orchestrator{Builds: rejectingBuildScheduler{err: &OperationError{
				Code: "build_rejected", Message: "build cannot be scheduled", Retryable: tc.retryable,
			}}}
			err := o.stepEnqueueBuild(context.Background(), SagaJob{}, uuid.New(), &SagaState{})
			var terminal *terminalError
			if got := errors.As(err, &terminal); got != tc.wantTerminal {
				t.Fatalf("terminal = %v, want %v (error %v)", got, tc.wantTerminal, err)
			}
		})
	}
}

func TestTTLMinutesEnforcesTierCeiling(t *testing.T) {
	cases := []struct {
		name string
		ttl  int
		max  int
		want int
	}{
		{"within ceiling", 1440, 1440, 1440},
		{"above ceiling is clamped", 10080, 1440, 1440},
		{"no ceiling configured", 10080, 0, 10080},
		{"unset ttl leaves runtime on its own default", 0, 1440, 0},
		{"negative ttl is treated as unset", -5, 1440, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Orchestrator{DeployTTLMinutes: tc.ttl, MaxTTLMinutes: tc.max}
			if got := o.ttlMinutes(); got != tc.want {
				t.Fatalf("ttlMinutes() = %d, want %d", got, tc.want)
			}
		})
	}
}

// --- alias promotion (Task 16a item 4) ------------------------------------
// Publishing happens after the deploy is running. That ordering
// is the whole guarantee: the previous build serves until the new one answers,
// so a redeploy is invisible from outside and a failed build takes nothing
// down. It also means a promotion failure arrives too late to be fatal.

type fakePromoter struct {
	moved  int
	err    error
	calls  int
	lastID uuid.UUID
}

func (f *fakePromoter) PromoteToDeploy(_ context.Context, deployID uuid.UUID) (int, error) {
	f.calls++
	f.lastID = deployID
	return f.moved, f.err
}

func promotingOrchestrator(p AliasPromoter) *Orchestrator {
	return &Orchestrator{Aliases: p, Log: zap.NewNop()}
}

func TestPromoteAliasesPublishesTheNewDeploy(t *testing.T) {
	p := &fakePromoter{moved: 1}
	id := uuid.New()

	promotingOrchestrator(p).promoteAliases(context.Background(), id)

	if p.calls != 1 {
		t.Fatalf("promoter called %d times, want 1", p.calls)
	}
	if p.lastID != id {
		t.Fatalf("promoted %s, want the deploy that just went live (%s)", p.lastID, id)
	}
}

// The deploy is running by the time this executes, so a bookkeeping failure
// must not be allowed to tear it down. The domain keeps
// serving the previous build — stale, not broken.
func TestPromoteAliasesSurvivesAFailure(t *testing.T) {
	p := &fakePromoter{err: errors.New("database is having a moment")}

	// The absence of a panic and of any returned error is the assertion:
	// promoteAliases has no way to fail the saga.
	promotingOrchestrator(p).promoteAliases(context.Background(), uuid.New())

	if p.calls != 1 {
		t.Fatalf("promoter called %d times, want 1", p.calls)
	}
}

// Promotion is opt-in by wiring. A deployment without the domain layer wired
// keeps the pre-16 behaviour of publishing by hand.
func TestPromoteAliasesIsSkippedWhenNotWired(t *testing.T) {
	(&Orchestrator{Log: zap.NewNop()}).promoteAliases(context.Background(), uuid.New())
}

// Nothing to move is the common case — most deploys have no custom domain —
// and must stay silent rather than announcing a publish that did not happen.
func TestPromoteAliasesSaysNothingWhenNoDomainMoved(t *testing.T) {
	p := &fakePromoter{moved: 0}
	o := promotingOrchestrator(p)
	o.Publisher = nil // a nil publisher would panic if promotion tried to announce

	o.promoteAliases(context.Background(), uuid.New())

	if p.calls != 1 {
		t.Fatalf("promoter called %d times, want 1", p.calls)
	}
}
