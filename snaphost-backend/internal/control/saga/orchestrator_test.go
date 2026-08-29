package saga

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestStatusErrorPermanence(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		wantPermanent bool
	}{
		// Task 15b: the container started and never answered. Retrying the
		// same image fails the same way; the reservation must be refunded.
		{"probe failure", http.StatusUnprocessableEntity, true},
		{"validation failure", http.StatusBadRequest, true},
		{"already running", http.StatusConflict, true},
		{"rate limited is worth waiting on", http.StatusTooManyRequests, false},
		{"runner down", http.StatusBadGateway, false},
		{"runner error", http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &StatusError{StatusCode: tc.status}
			if got := err.Permanent(); got != tc.wantPermanent {
				t.Fatalf("Permanent() = %v for %d, want %v", got, tc.status, tc.wantPermanent)
			}
		})
	}
}

func TestStatusErrorUserReasonPrefersServerMessage(t *testing.T) {
	withMessage := &StatusError{
		StatusCode: http.StatusUnprocessableEntity,
		Code:       "probe_failed",
		Message:    "the container started but nothing answered on the port the runtime injects.",
	}
	if withMessage.UserReason() != withMessage.Message {
		t.Fatalf("UserReason() = %q, want the downstream message verbatim", withMessage.UserReason())
	}

	bare := &StatusError{StatusCode: http.StatusUnprocessableEntity}
	if bare.UserReason() == "" {
		t.Fatal("a reason is shown to the deploy owner; it must never be empty")
	}
}

func TestStatusErrorIsMatchable(t *testing.T) {
	var target *StatusError
	wrapped := errors.Join(errors.New("runner deploy"), &StatusError{StatusCode: http.StatusUnprocessableEntity})
	if !errors.As(wrapped, &target) {
		t.Fatal("the orchestrator matches with errors.As; a wrapped StatusError must still be found")
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
		{"unset ttl leaves runner on its own default", 0, 1440, 0},
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
// Publishing happens after the deploy is running and paid for. That ordering
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

// The deploy is running and the coins are committed by the time this runs, so
// a bookkeeping failure must not be allowed to tear it down. The domain keeps
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
