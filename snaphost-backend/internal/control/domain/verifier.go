package domain

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// checker is the DNS surface the verifier needs; the tests substitute it.
type checker interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// verifierStore is the persistence surface the verifier needs.
type verifierStore interface {
	DueForCheck(ctx context.Context, reverifyAfter time.Duration, limit int) ([]Domain, error)
	MarkVerified(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, reason string) error
	TouchChecked(ctx context.Context, id uuid.UUID, reason string) error
}

// Verifier resolves the TXT challenge for attached domains: it flips pending
// domains to verified, and re-checks verified ones so a domain that stops
// pointing at us — or is transferred to someone else — loses verification
// instead of being served forever.
type Verifier struct {
	store    verifierStore
	resolver checker
	log      *zap.Logger

	// Interval is how often the sweep runs.
	Interval time.Duration
	// ReverifyAfter is how stale a verified domain's last check may get.
	ReverifyAfter time.Duration
	// BatchSize bounds one sweep, so a large account cannot monopolize it.
	BatchSize int
	// PropagationGrace is how long a never-verified domain may go without its
	// TXT record before that counts as a failure rather than as DNS still
	// propagating. Inside the window the domain stays `pending`, which is what
	// is actually true; outside it, the user has most likely not added the
	// record at all and deserves to be told so.
	PropagationGrace time.Duration
}

// NewVerifier builds a Verifier over the system resolver.
func NewVerifier(store verifierStore, log *zap.Logger, interval, reverifyAfter, propagationGrace time.Duration, batchSize int) *Verifier {
	return &Verifier{
		store:            store,
		resolver:         &net.Resolver{},
		log:              log,
		Interval:         interval,
		ReverifyAfter:    reverifyAfter,
		BatchSize:        batchSize,
		PropagationGrace: propagationGrace,
	}
}

// Run sweeps on a ticker until the context is cancelled.
func (v *Verifier) Run(ctx context.Context) {
	ticker := time.NewTicker(v.Interval)
	defer ticker.Stop()

	v.log.Info("domain verifier started",
		zap.Duration("interval", v.Interval),
		zap.Duration("reverify_after", v.ReverifyAfter))

	for {
		select {
		case <-ctx.Done():
			v.log.Info("domain verifier stopping")
			return
		case <-ticker.C:
			v.Sweep(ctx)
		}
	}
}

// Sweep runs one verification pass.
func (v *Verifier) Sweep(ctx context.Context) {
	due, err := v.store.DueForCheck(ctx, v.ReverifyAfter, v.BatchSize)
	if err != nil {
		v.log.Error("domain verifier: list due domains failed", zap.Error(err))
		return
	}
	for _, d := range due {
		v.check(ctx, d)
	}
}

func (v *Verifier) check(ctx context.Context, d Domain) {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	records, err := v.resolver.LookupTXT(lookupCtx, d.ChallengeRecord())
	if err != nil {
		var dnsErr *net.DNSError
		// NXDOMAIN / no such host is a real answer: the record is not there.
		// Anything else (timeout, server failure) proves nothing, so an
		// already-verified domain must not lose verification over it.
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			// A record that is not there yet is the normal state of a domain
			// attached a minute ago: DNS takes minutes, sometimes hours. Calling
			// that a failure tells the user something is broken when nothing is,
			// and "pending" is simply the truer description. Hold that line only
			// while the domain has never verified and is still inside the grace
			// window — a domain that *was* verified and has stopped proving it
			// must fail immediately, because it is still routing traffic.
			if v.stillPropagating(d) {
				if err := v.store.TouchChecked(ctx, d.ID, ReasonTXTNotFound); err != nil {
					v.log.Warn("domain verifier: touch failed",
						zap.String("domain", d.Domain), zap.Error(err))
				}
				return
			}
			v.fail(ctx, d, ReasonTXTNotFound)
			return
		}
		v.log.Warn("domain verifier: DNS lookup failed",
			zap.String("domain", d.Domain), zap.Error(err))
		if err := v.store.TouchChecked(ctx, d.ID, ReasonDNSLookupFailed); err != nil {
			v.log.Warn("domain verifier: touch failed", zap.String("domain", d.Domain), zap.Error(err))
		}
		return
	}

	for _, record := range records {
		if strings.TrimSpace(record) == d.VerificationToken {
			if d.Status == StatusVerified {
				// Still ours; just record the successful check.
				if err := v.store.MarkVerified(ctx, d.ID); err != nil {
					v.log.Warn("domain verifier: re-verify write failed",
						zap.String("domain", d.Domain), zap.Error(err))
				}
				return
			}
			if err := v.store.MarkVerified(ctx, d.ID); err != nil {
				v.log.Error("domain verifier: mark verified failed",
					zap.String("domain", d.Domain), zap.Error(err))
				return
			}
			v.log.Info("custom domain verified", zap.String("domain", d.Domain))
			return
		}
	}

	v.log.Info("custom domain TXT value did not match",
		zap.String("domain", d.Domain), zap.String("record", d.ChallengeRecord()))
	v.fail(ctx, d, ReasonTXTMismatch)
}

// stillPropagating reports whether a missing TXT record is better explained by
// DNS not having caught up than by the user not having added it.
//
// Deliberately narrow. A domain that has ever verified is excluded whatever its
// age: losing the record there is the dangling-record takeover case, and it has
// to stop routing at once. A mismatched value is excluded too, elsewhere — the
// record exists and is wrong, which is actionable now rather than a wait.
func (v *Verifier) stillPropagating(d Domain) bool {
	if d.VerifiedAt != nil || d.Status == StatusVerified {
		return false
	}
	if v.PropagationGrace <= 0 {
		return false
	}
	return time.Since(d.CreatedAt) < v.PropagationGrace
}

func (v *Verifier) fail(ctx context.Context, d Domain, reason string) {
	if err := v.store.MarkFailed(ctx, d.ID, reason); err != nil {
		v.log.Error("domain verifier: mark failed", zap.String("domain", d.Domain), zap.Error(err))
		return
	}
	if d.Status == StatusVerified {
		// This is the takeover case: the domain was serving traffic and has
		// just stopped proving it is ours.
		v.log.Warn("custom domain lost verification and stopped routing",
			zap.String("domain", d.Domain), zap.String("reason", reason))
	}
}
