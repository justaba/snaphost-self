package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/billing"
)

// probingBackend is a fakeBackend that also implements backend.Prober, so the
// service takes the probe path.
type probingBackend struct {
	fakeBackend
	probeErr   error
	probeCalls *int
}

func (b probingBackend) Probe(context.Context, backend.ProbeRequest) error {
	if b.probeCalls != nil {
		(*b.probeCalls)++
	}
	return b.probeErr
}

// runningBilling records whether the deploy was ever reported as running, which
// is the whole point of Task 15b: a deploy that does not answer must not be.
type runningBilling struct {
	deployBilling
	runningCalls int
	statuses     []string
	reasons      []string
}

func (b *runningBilling) SetDeployRunning(context.Context, string, billing.SetRunningRequest) error {
	b.runningCalls++
	return nil
}

func (b *runningBilling) UpdateDeployStatus(_ context.Context, _ string, status string, reason *string) error {
	b.statuses = append(b.statuses, status)
	if reason != nil {
		b.reasons = append(b.reasons, *reason)
	}
	return nil
}

func probeCfg(enabled bool) *config.Config {
	cfg := looseCfg()
	cfg.RuntimeProbeEnabled = enabled
	cfg.RuntimeProbeTimeoutSec = 5
	cfg.ContainerDefaultTTLMin = 30
	cfg.DomainSuffix = "localhost"
	return cfg
}

func newDeployingSvc(b backend.Backend, bc BillingClient, cfg *config.Config) *Service {
	return &Service{
		backend:   b,
		billing:   bc,
		publisher: &recordingPublisher{},
		cfg:       cfg,
		log:       zap.NewNop(),
	}
}

func TestDeployRefusesToReportRunningWhenProbeFails(t *testing.T) {
	stopCalls, probeCalls := 0, 0
	bc := &runningBilling{deployBilling: deployBilling{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{
			fakeBackend: fakeBackend{stopCalls: &stopCalls},
			probeErr:    backend.ErrProbeFailed,
			probeCalls:  &probeCalls,
		},
		bc,
		probeCfg(true),
	)

	_, err := svc.Deploy(context.Background(), req())

	var perr *ProbeError
	if !errors.As(err, &perr) {
		t.Fatalf("want ProbeError, got %T: %v", err, err)
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want 1", probeCalls)
	}
	if bc.runningCalls != 0 {
		t.Fatal("a deploy that never answered must not be reported running — that is what billed a dead URL on 2026-07-19")
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1: the runtime that will never serve has to be torn down", stopCalls)
	}
	if len(bc.statuses) == 0 || bc.statuses[len(bc.statuses)-1] != "failed" {
		t.Fatalf("statuses = %v, want the deploy marked failed", bc.statuses)
	}
	if len(bc.reasons) == 0 || !strings.Contains(bc.reasons[len(bc.reasons)-1], "PORT") {
		t.Fatalf("reasons = %v, want a reason naming PORT", bc.reasons)
	}
}

func TestDeployReportsRunningWhenProbePasses(t *testing.T) {
	stopCalls, probeCalls := 0, 0
	bc := &runningBilling{deployBilling: deployBilling{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{
			fakeBackend: fakeBackend{stopCalls: &stopCalls},
			probeCalls:  &probeCalls,
		},
		bc,
		probeCfg(true),
	)

	result, err := svc.Deploy(context.Background(), req())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ContainerID != "container-id" {
		t.Fatalf("container id = %q", result.ContainerID)
	}
	if probeCalls != 1 || bc.runningCalls != 1 {
		t.Fatalf("probe calls = %d, running calls = %d, want 1 and 1", probeCalls, bc.runningCalls)
	}
	if stopCalls != 0 {
		t.Fatal("a healthy deploy must not be torn down")
	}
}

func TestDeploySkipsProbeWhenDisabled(t *testing.T) {
	probeCalls := 0
	bc := &runningBilling{deployBilling: deployBilling{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{probeErr: backend.ErrProbeFailed, probeCalls: &probeCalls},
		bc,
		probeCfg(false),
	)

	if _, err := svc.Deploy(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probeCalls != 0 {
		t.Fatalf("probe calls = %d, want 0 when RUNTIME_PROBE_ENABLED=false", probeCalls)
	}
	if bc.runningCalls != 1 {
		t.Fatal("with the probe off the pre-15 behavior stands: started means running")
	}
}

// A backend with no Prober implementation must deploy exactly as before rather
// than fail closed — the probe is a check we added, not a contract backends
// must satisfy to work at all.
func TestDeployWithoutProberProceeds(t *testing.T) {
	bc := &runningBilling{deployBilling: deployBilling{info: deployableInfo()}}
	svc := newDeployingSvc(fakeBackend{}, bc, probeCfg(true))

	if _, err := svc.Deploy(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bc.runningCalls != 1 {
		t.Fatalf("running calls = %d, want 1", bc.runningCalls)
	}
}
