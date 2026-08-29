package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/runtime/backend"
	"snaphost/internal/runtime/billing"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/logs"
)

// fakeBilling implements BillingClient. Only GetDeploy is exercised by
// validation tests; the other methods panic so accidental calls surface
// in tests rather than hiding behind no-ops.
type fakeBilling struct {
	info *billing.DeployInfo
	err  error
}

func (f *fakeBilling) GetDeploy(_ context.Context, _ string) (*billing.DeployInfo, error) {
	return f.info, f.err
}
func (f *fakeBilling) UpdateDeployStatus(context.Context, string, string, *string) error {
	panic("not used")
}
func (f *fakeBilling) SetDeployRunning(context.Context, string, billing.SetRunningRequest) error {
	panic("not used")
}

const (
	testDeployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"
	testUserID   = "a4a355f8-9769-454f-b5c0-9782acceebc0"
	testPrefix   = "host.docker.internal:5000/snaphost"
	testImageRef = testPrefix + "/proj-x:" + testDeployID
)

func newSvc(t *testing.T, cfg *config.Config, b BillingClient) *Service {
	t.Helper()
	return &Service{
		cfg:     cfg,
		billing: b,
		log:     zap.NewNop(),
	}
}

type recordingPublisher struct {
	lines []logs.LogLine
}

func (p *recordingPublisher) Publish(deployID string, line logs.LogLine) error {
	line.DeployID = deployID
	p.lines = append(p.lines, line)
	return nil
}

func (p *recordingPublisher) Close() error { return nil }

type deployBilling struct {
	info *billing.DeployInfo
	err  error
}

func (b *deployBilling) GetDeploy(context.Context, string) (*billing.DeployInfo, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.info, nil
}
func (b *deployBilling) UpdateDeployStatus(context.Context, string, string, *string) error {
	return nil
}
func (b *deployBilling) SetDeployRunning(context.Context, string, billing.SetRunningRequest) error {
	return nil
}

type fakeBackend struct {
	runErr    error
	stopErr   error
	stopCalls *int
}

func (b fakeBackend) Run(context.Context, backend.RunRequest) (*backend.RunResult, error) {
	if b.runErr != nil {
		return nil, b.runErr
	}
	now := time.Now().UTC()
	return &backend.RunResult{
		ContainerID:  "container-id",
		EndpointURL:  "https://proj.example.test",
		StartedAt:    now,
		TTLExpiresAt: now.Add(time.Minute),
	}, nil
}
func (b fakeBackend) Stop(context.Context, string, string) error {
	if b.stopCalls != nil {
		(*b.stopCalls)++
	}
	return b.stopErr
}
func (b fakeBackend) HealthCheck(context.Context, string) (*backend.HealthStatus, error) {
	return &backend.HealthStatus{Running: true}, nil
}
func (b fakeBackend) StreamLogs(context.Context, string) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}
func (b fakeBackend) Name() string { return "fake" }

func strictCfg() *config.Config {
	return &config.Config{
		AllowedRegistryPrefixes: []string{testPrefix},
		StrictImageValidation:   true,
	}
}

func looseCfg() *config.Config {
	return &config.Config{
		AllowedRegistryPrefixes: []string{testPrefix},
		StrictImageValidation:   false,
	}
}

// deployableInfo returns a DeployInfo in the canonical "ready to deploy"
// state. Status is "building" — the only value in deployableStatuses.
// See deploys.status state machine in user-billing migrations.
func deployableInfo() *billing.DeployInfo {
	return &billing.DeployInfo{
		DeployID:    testDeployID,
		UserID:      testUserID,
		Status:      "building",
		ImageRef:    testImageRef,
		ContainerID: "container-id",
	}
}

func req() DeployRequest {
	return DeployRequest{
		DeployID: testDeployID,
		UserID:   testUserID,
		ImageRef: testImageRef,
	}
}

func TestValidate_Happy(t *testing.T) {
	svc := newSvc(t, strictCfg(), &fakeBilling{info: deployableInfo()})
	if err := svc.validateDeployRequest(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_WrongRegistryPrefix(t *testing.T) {
	r := req()
	r.ImageRef = "evil.example.com/snaphost/proj-x:" + testDeployID
	svc := newSvc(t, strictCfg(), &fakeBilling{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_TagMismatch(t *testing.T) {
	r := req()
	r.ImageRef = testPrefix + "/proj-x:something-else"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_MissingTag(t *testing.T) {
	r := req()
	r.ImageRef = testPrefix + "/proj-x"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_UserIDMismatch(t *testing.T) {
	info := deployableInfo()
	info.UserID = "11111111-1111-1111-1111-111111111111"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_AlreadyRunning(t *testing.T) {
	info := deployableInfo()
	info.Status = "running"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestValidate_StatusDeleted(t *testing.T) {
	info := deployableInfo()
	info.Status = "deleted"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_StatusFailed(t *testing.T) {
	info := deployableInfo()
	info.Status = "failed"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_StatusPending(t *testing.T) {
	info := deployableInfo()
	info.Status = "pending"
	svc := newSvc(t, strictCfg(), &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_DeployNotFound(t *testing.T) {
	svc := newSvc(t, strictCfg(), &fakeBilling{err: billing.ErrDeployNotFound})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_BillingTransientError(t *testing.T) {
	svc := newSvc(t, strictCfg(), &fakeBilling{err: fmt.Errorf("billing returned 503: ...")})
	err := svc.validateDeployRequest(context.Background(), req())
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("want ErrTransient, got %v", err)
	}
	var verr *ValidationError
	if errors.As(err, &verr) {
		t.Fatalf("transient should NOT be a ValidationError")
	}
}

func TestValidate_LooseModeSkipsBilling(t *testing.T) {
	// fakeBilling.GetDeploy would panic if called (info nil, err nil, but
	// strict path is the only code path that calls it). Verify that loose
	// mode never touches it: use a billing that fails the assertion.
	called := false
	fb := assertingBilling{onGet: func() { called = true }}
	svc := newSvc(t, looseCfg(), &fb)
	err := svc.validateDeployRequest(context.Background(), req())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("billing.GetDeploy should not be called in loose mode")
	}
}

func TestValidate_LooseModeStillEnforcesPrefixAndTag(t *testing.T) {
	r := req()
	r.ImageRef = "evil.example.com/x:" + testDeployID
	svc := newSvc(t, looseCfg(), assertingBilling{onGet: func() { t.Fatal("billing should not be called") }})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError for prefix mismatch in loose mode, got %T: %v", err, err)
	}
}

func TestImageRefMatchesAllowedPrefix(t *testing.T) {
	cases := []struct {
		name     string
		imageRef string
		prefix   string
		want     bool
	}{
		{
			name:     "yandex production prefix",
			imageRef: "cr.yandex/crp123/snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "cr.yandex/crp123/snaphost",
			want:     true,
		},
		{
			name:     "yandex production prefix with trailing slash",
			imageRef: "cr.yandex/crp123/snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "cr.yandex/crp123/snaphost/",
			want:     true,
		},
		{
			name:     "docker dev prefix",
			imageRef: "registry:5000/snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "registry:5000/snaphost",
			want:     true,
		},
		{
			name:     "reject shared textual prefix without path boundary",
			imageRef: "cr.yandex/crp123/snaphostevil/proj-abcdef12:" + testDeployID,
			prefix:   "cr.yandex/crp123/snaphost",
			want:     false,
		},
		{
			name:     "reject empty prefix",
			imageRef: "cr.yandex/crp123/snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "",
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageRefMatchesAllowedPrefix(tc.imageRef, tc.prefix); got != tc.want {
				t.Fatalf("imageRefMatchesAllowedPrefix() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidate_YandexProductionPrefix(t *testing.T) {
	cfg := &config.Config{
		AllowedRegistryPrefixes: []string{"cr.yandex/crp123/snaphost"},
		StrictImageValidation:   true,
	}
	imageRef := "cr.yandex/crp123/snaphost/proj-abcdef12:" + testDeployID
	info := deployableInfo()
	info.ImageRef = imageRef
	svc := newSvc(t, cfg, &fakeBilling{info: info})
	err := svc.validateDeployRequest(context.Background(), DeployRequest{
		DeployID: testDeployID,
		UserID:   testUserID,
		ImageRef: imageRef,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeployPublishesLifecycleLogs(t *testing.T) {
	pub := &recordingPublisher{}
	svc := NewService(
		fakeBackend{},
		&deployBilling{info: deployableInfo()},
		pub,
		strictCfg(),
		zap.NewNop(),
	)

	_, err := svc.Deploy(context.Background(), req())
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}

	want := []string{
		"deploy accepted by runner",
		"image validation passed",
		"preparing deployment",
		"backend run started: fake",
		"public URL ready: https://proj.example.test",
	}
	assertLogTexts(t, pub.lines, want)
}

func TestDeployPublishesValidationFailureLog(t *testing.T) {
	pub := &recordingPublisher{}
	svc := NewService(fakeBackend{}, &deployBilling{info: deployableInfo()}, pub, strictCfg(), zap.NewNop())
	r := req()
	r.ImageRef = "evil.example.com/proj-x:" + testDeployID

	err := func() error {
		_, err := svc.Deploy(context.Background(), r)
		return err
	}()
	if err == nil {
		t.Fatal("Deploy() error = nil, want validation error")
	}

	assertLogContains(t, pub.lines, "image validation failed:")
}

func TestDeployPublishesBackendFailureLog(t *testing.T) {
	pub := &recordingPublisher{}
	svc := NewService(
		fakeBackend{runErr: fmt.Errorf("provider failed\nwith token=redacted?")},
		&deployBilling{info: deployableInfo()},
		pub,
		strictCfg(),
		zap.NewNop(),
	)

	_, err := svc.Deploy(context.Background(), req())
	if err == nil {
		t.Fatal("Deploy() error = nil, want backend error")
	}

	assertLogContains(t, pub.lines, "backend run failed: provider failed with token=[redacted]")
}

func TestUserVisibleErrorCompactsAndTruncates(t *testing.T) {
	err := errors.New(strings.Repeat("a", 300) + "\nsecret-ish tail")
	got := userVisibleError(err)
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("userVisibleError() did not compact newlines: %q", got)
	}
	if len(got) > 243 {
		t.Fatalf("userVisibleError() length = %d, want <= 243", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("userVisibleError() = %q, want truncation suffix", got)
	}
}

func TestUserVisibleErrorRedactsSensitiveFragments(t *testing.T) {
	got := userVisibleError(errors.New("request failed token=abc123 Authorization: Bearer very-secret password: hunter2"))
	if strings.Contains(got, "abc123") || strings.Contains(got, "very-secret") || strings.Contains(got, "hunter2") {
		t.Fatalf("userVisibleError() did not redact sensitive fragments: %q", got)
	}
	if !strings.Contains(got, "token=[redacted]") || !strings.Contains(got, "Authorization=[redacted]") || !strings.Contains(got, "password=[redacted]") {
		t.Fatalf("userVisibleError() missing redaction markers: %q", got)
	}
}

func TestUndeployPublishesStopFailureLog(t *testing.T) {
	pub := &recordingPublisher{}
	svc := NewService(
		fakeBackend{stopErr: errors.New("delete failed\nraw details")},
		&deployBilling{info: deployableInfo()},
		pub,
		strictCfg(),
		zap.NewNop(),
	)

	err := svc.Undeploy(context.Background(), testDeployID, "container-id")
	if err == nil {
		t.Fatal("Undeploy() error = nil, want stop error")
	}

	assertLogContains(t, pub.lines, "stopping deployment")
	assertLogContains(t, pub.lines, "stop failed: delete failed raw details")
}

func TestUndeployMatchingStoredContainerAllowsStop(t *testing.T) {
	calls := 0
	svc := NewService(
		fakeBackend{stopCalls: &calls},
		&deployBilling{info: deployableInfo()},
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	if err := svc.Undeploy(context.Background(), testDeployID, "container-id"); err != nil {
		t.Fatalf("Undeploy() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("backend Stop calls = %d, want 1", calls)
	}
}

func TestUndeployMismatchedContainerRejectedBeforeStop(t *testing.T) {
	calls := 0
	svc := NewService(
		fakeBackend{stopCalls: &calls},
		&deployBilling{info: deployableInfo()},
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	err := svc.Undeploy(context.Background(), testDeployID, "attacker-container-id")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
	if calls != 0 {
		t.Fatalf("backend Stop calls = %d, want 0", calls)
	}
}

func TestUndeployMissingDeployRejectedBeforeStop(t *testing.T) {
	calls := 0
	svc := NewService(
		fakeBackend{stopCalls: &calls},
		&deployBilling{err: billing.ErrDeployNotFound},
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	err := svc.Undeploy(context.Background(), testDeployID, "container-id")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
	if calls != 0 {
		t.Fatalf("backend Stop calls = %d, want 0", calls)
	}
}

func TestUndeployEmptyStoredContainerRejectedBeforeStop(t *testing.T) {
	calls := 0
	info := deployableInfo()
	info.ContainerID = ""
	svc := NewService(
		fakeBackend{stopCalls: &calls},
		&deployBilling{info: info},
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	err := svc.Undeploy(context.Background(), testDeployID, "container-id")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
	if calls != 0 {
		t.Fatalf("backend Stop calls = %d, want 0", calls)
	}
}

func TestUndeployBackendStopFailurePropagatesAfterOwnershipCheck(t *testing.T) {
	calls := 0
	stopErr := errors.New("provider stop failed")
	svc := NewService(
		fakeBackend{stopCalls: &calls, stopErr: stopErr},
		&deployBilling{info: deployableInfo()},
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	err := svc.Undeploy(context.Background(), testDeployID, "container-id")
	if !errors.Is(err, stopErr) {
		t.Fatalf("want stopErr, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("backend Stop calls = %d, want 1", calls)
	}
}

func TestStopExpiredPublishesTTLLogs(t *testing.T) {
	pub := &recordingPublisher{}
	svc := NewService(fakeBackend{}, &deployBilling{info: deployableInfo()}, pub, strictCfg(), zap.NewNop())

	if err := svc.StopExpired(context.Background(), testDeployID, "container-id"); err != nil {
		t.Fatalf("StopExpired() error = %v", err)
	}

	want := []string{
		"watchdog TTL cleanup started",
		"stopping deployment",
		"stopped cleanly",
		"watchdog TTL cleanup succeeded",
	}
	assertLogTexts(t, pub.lines, want)
}

func assertLogTexts(t *testing.T, lines []logs.LogLine, want []string) {
	t.Helper()
	got := make([]string, 0, len(lines))
	for _, line := range lines {
		got = append(got, line.Text)
	}
	for _, text := range want {
		found := false
		for _, gotText := range got {
			if gotText == text {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing log text %q in %v", text, got)
		}
	}
}

func assertLogContains(t *testing.T, lines []logs.LogLine, needle string) {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line.Text, needle) {
			return
		}
	}
	var got []string
	for _, line := range lines {
		got = append(got, line.Text)
	}
	t.Fatalf("missing log containing %q in %v", needle, got)
}

// assertingBilling lets a test fail when GetDeploy is unexpectedly called.
type assertingBilling struct {
	onGet func()
}

func (a assertingBilling) GetDeploy(context.Context, string) (*billing.DeployInfo, error) {
	if a.onGet != nil {
		a.onGet()
	}
	return nil, errors.New("should not be called")
}
func (a assertingBilling) UpdateDeployStatus(context.Context, string, string, *string) error {
	panic("not used")
}
func (a assertingBilling) SetDeployRunning(context.Context, string, billing.SetRunningRequest) error {
	panic("not used")
}
