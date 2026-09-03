package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"snaphost/internal/runtime/backend"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/deployments"
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

// runningDeploymentStore records whether the deploy was ever reported as
// running; a deploy that does not answer must never reach that state.
type runningDeploymentStore struct {
	recordingDeploymentStore
	runningCalls int
	statuses     []string
	reasons      []string
}

func (b *runningDeploymentStore) SetDeployRunning(context.Context, string, deployments.SetRunningRequest) error {
	b.runningCalls++
	return nil
}

func (b *runningDeploymentStore) UpdateDeployStatus(_ context.Context, _ string, status string, reason *string) error {
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

func newDeployingSvc(b backend.Backend, store DeploymentStore, cfg *config.Config) *Service {
	return &Service{
		backend:   b,
		deploys:   store,
		publisher: &recordingPublisher{},
		cfg:       cfg,
		log:       zap.NewNop(),
	}
}

func TestDeployRefusesToReportRunningWhenProbeFails(t *testing.T) {
	stopCalls, probeCalls := 0, 0
	store := &runningDeploymentStore{recordingDeploymentStore: recordingDeploymentStore{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{
			fakeBackend: fakeBackend{stopCalls: &stopCalls},
			probeErr:    backend.ErrProbeFailed,
			probeCalls:  &probeCalls,
		},
		store,
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
	if store.runningCalls != 0 {
		t.Fatal("a deploy that never answered must not be reported running")
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1: the runtime that will never serve has to be torn down", stopCalls)
	}
	if len(store.statuses) == 0 || store.statuses[len(store.statuses)-1] != "failed" {
		t.Fatalf("statuses = %v, want the deploy marked failed", store.statuses)
	}
	if len(store.reasons) == 0 || !strings.Contains(store.reasons[len(store.reasons)-1], "PORT") {
		t.Fatalf("reasons = %v, want a reason naming PORT", store.reasons)
	}
}

func TestDeployReportsRunningWhenProbePasses(t *testing.T) {
	stopCalls, probeCalls := 0, 0
	store := &runningDeploymentStore{recordingDeploymentStore: recordingDeploymentStore{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{
			fakeBackend: fakeBackend{stopCalls: &stopCalls},
			probeCalls:  &probeCalls,
		},
		store,
		probeCfg(true),
	)

	result, err := svc.Deploy(context.Background(), req())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ContainerID != "container-id" {
		t.Fatalf("container id = %q", result.ContainerID)
	}
	if probeCalls != 1 || store.runningCalls != 1 {
		t.Fatalf("probe calls = %d, running calls = %d, want 1 and 1", probeCalls, store.runningCalls)
	}
	if stopCalls != 0 {
		t.Fatal("a healthy deploy must not be torn down")
	}
}

func TestDeploySkipsProbeWhenDisabled(t *testing.T) {
	probeCalls := 0
	store := &runningDeploymentStore{recordingDeploymentStore: recordingDeploymentStore{info: deployableInfo()}}
	svc := newDeployingSvc(
		probingBackend{probeErr: backend.ErrProbeFailed, probeCalls: &probeCalls},
		store,
		probeCfg(false),
	)

	if _, err := svc.Deploy(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probeCalls != 0 {
		t.Fatalf("probe calls = %d, want 0 when RUNTIME_PROBE_ENABLED=false", probeCalls)
	}
	if store.runningCalls != 1 {
		t.Fatal("with the probe disabled, a started container should be reported running")
	}
}

// A backend with no Prober implementation remains valid because probing is an
// optional capability.
func TestDeployWithoutProberProceeds(t *testing.T) {
	store := &runningDeploymentStore{recordingDeploymentStore: recordingDeploymentStore{info: deployableInfo()}}
	svc := newDeployingSvc(fakeBackend{}, store, probeCfg(true))

	if _, err := svc.Deploy(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.runningCalls != 1 {
		t.Fatalf("running calls = %d, want 1", store.runningCalls)
	}
}
