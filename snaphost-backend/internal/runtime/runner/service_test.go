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
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/deployments"
	"snaphost/internal/runtime/logs"
)

// fakeDeploymentStore implements DeploymentStore. Only GetDeploy is exercised by
// validation tests; the other methods panic so accidental calls surface
// in tests rather than hiding behind no-ops.
type fakeDeploymentStore struct {
	info *deployments.Info
	err  error
}

func (f *fakeDeploymentStore) GetDeploy(_ context.Context, _ string) (*deployments.Info, error) {
	return f.info, f.err
}
func (f *fakeDeploymentStore) UpdateDeployStatus(context.Context, string, string, *string) error {
	panic("not used")
}
func (f *fakeDeploymentStore) SetDeployRunning(context.Context, string, deployments.SetRunningRequest) error {
	panic("not used")
}
func (f *fakeDeploymentStore) MarkDeployImageDeleted(context.Context, string) error {
	panic("not used")
}

const (
	testDeployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"
	testUserID   = "a4a355f8-9769-454f-b5c0-9782acceebc0"
	testPrefix   = "snaphost"
	testImageRef = testPrefix + "/proj-x:" + testDeployID
)

func newSvc(t *testing.T, cfg *config.Config, store DeploymentStore) *Service {
	t.Helper()
	return &Service{
		cfg:     cfg,
		deploys: store,
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

type recordingDeploymentStore struct {
	info                 *deployments.Info
	err                  error
	setRunningErr        error
	statuses             []string
	statusContextErrs    []error
	setRunningContextErr *error
	markImageErr         error
	markedImages         []string
}

func (b *recordingDeploymentStore) GetDeploy(context.Context, string) (*deployments.Info, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.info, nil
}
func (b *recordingDeploymentStore) UpdateDeployStatus(ctx context.Context, _ string, status string, _ *string) error {
	b.statuses = append(b.statuses, status)
	b.statusContextErrs = append(b.statusContextErrs, ctx.Err())
	return nil
}
func (b *recordingDeploymentStore) SetDeployRunning(ctx context.Context, _ string, _ deployments.SetRunningRequest) error {
	if b.setRunningContextErr != nil {
		*b.setRunningContextErr = ctx.Err()
	}
	return b.setRunningErr
}
func (b *recordingDeploymentStore) MarkDeployImageDeleted(_ context.Context, deployID string) error {
	b.markedImages = append(b.markedImages, deployID)
	return b.markImageErr
}

type fakeBackend struct {
	runErr         error
	stopErr        error
	stopCalls      *int
	stopContextErr *error
	onRun          func()
	removeImageErr error
	removedImages  *[]string
}

func (b fakeBackend) Run(context.Context, backend.RunRequest) (*backend.RunResult, error) {
	if b.onRun != nil {
		b.onRun()
	}
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
func (b fakeBackend) Stop(ctx context.Context, _ string, _ string) error {
	if b.stopCalls != nil {
		(*b.stopCalls)++
	}
	if b.stopContextErr != nil {
		*b.stopContextErr = ctx.Err()
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
func (b fakeBackend) RemoveImage(_ context.Context, imageRef string) error {
	if b.removedImages != nil {
		*b.removedImages = append(*b.removedImages, imageRef)
	}
	return b.removeImageErr
}
func (b fakeBackend) Name() string { return "fake" }

func strictCfg() *config.Config {
	return &config.Config{
		AllowedImagePrefixes:  []string{testPrefix},
		StrictImageValidation: true,
	}
}

func looseCfg() *config.Config {
	return &config.Config{
		AllowedImagePrefixes:  []string{testPrefix},
		StrictImageValidation: false,
	}
}

// deployableInfo returns a DeployInfo in the canonical "ready to deploy"
// state. Status is "building" — the only value in deployableStatuses.
// See the deploys.status state machine in control migrations.
func deployableInfo() *deployments.Info {
	return &deployments.Info{
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
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: deployableInfo()})
	if err := svc.validateDeployRequest(context.Background(), req()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_WrongRegistryPrefix(t *testing.T) {
	r := req()
	r.ImageRef = "evil.example.com/snaphost/proj-x:" + testDeployID
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_TagMismatch(t *testing.T) {
	r := req()
	r.ImageRef = testPrefix + "/proj-x:something-else"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_MissingTag(t *testing.T) {
	r := req()
	r.ImageRef = testPrefix + "/proj-x"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: deployableInfo()})
	err := svc.validateDeployRequest(context.Background(), r)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_UserIDMismatch(t *testing.T) {
	info := deployableInfo()
	info.UserID = "11111111-1111-1111-1111-111111111111"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_AlreadyRunning(t *testing.T) {
	info := deployableInfo()
	info.Status = "running"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestValidate_StatusDeleted(t *testing.T) {
	info := deployableInfo()
	info.Status = "deleted"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_StatusFailed(t *testing.T) {
	info := deployableInfo()
	info.Status = "failed"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_StatusPending(t *testing.T) {
	info := deployableInfo()
	info.Status = "pending"
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{info: info})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_DeployNotFound(t *testing.T) {
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{err: deployments.ErrNotFound})
	err := svc.validateDeployRequest(context.Background(), req())
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_DeploymentStoreTransientError(t *testing.T) {
	svc := newSvc(t, strictCfg(), &fakeDeploymentStore{err: fmt.Errorf("database temporarily unavailable: ...")})
	err := svc.validateDeployRequest(context.Background(), req())
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("want ErrTransient, got %v", err)
	}
	var verr *ValidationError
	if errors.As(err, &verr) {
		t.Fatalf("transient should NOT be a ValidationError")
	}
}

func TestValidate_LooseModeSkipsDeploymentLookup(t *testing.T) {
	// fakeDeploymentStore.GetDeploy would panic if called (info nil, err nil, but
	// strict path is the only code path that calls it). Verify that loose
	// mode never touches it: use a deployment store that fails the assertion.
	called := false
	fb := assertingDeploymentStore{onGet: func() { called = true }}
	svc := newSvc(t, looseCfg(), &fb)
	err := svc.validateDeployRequest(context.Background(), req())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("deploymentStore.GetDeploy should not be called in loose mode")
	}
}

func TestValidate_LooseModeStillEnforcesPrefixAndTag(t *testing.T) {
	r := req()
	r.ImageRef = "evil.example.com/x:" + testDeployID
	svc := newSvc(t, looseCfg(), assertingDeploymentStore{onGet: func() { t.Fatal("deployment store should not be called") }})
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
			name:     "configured namespace",
			imageRef: "snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "snaphost",
			want:     true,
		},
		{
			name:     "configured namespace with trailing slash",
			imageRef: "snaphost/proj-abcdef12:" + testDeployID,
			prefix:   "snaphost/",
			want:     true,
		},
		{
			name:     "reject shared textual prefix without path boundary",
			imageRef: "snaphostevil/proj-abcdef12:" + testDeployID,
			prefix:   "snaphost",
			want:     false,
		},
		{
			name:     "reject empty prefix",
			imageRef: "snaphost/proj-abcdef12:" + testDeployID,
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

func TestValidate_ConfiguredImageNamespace(t *testing.T) {
	cfg := &config.Config{
		AllowedImagePrefixes:  []string{"snaphost"},
		StrictImageValidation: true,
	}
	imageRef := "snaphost/proj-abcdef12:" + testDeployID
	info := deployableInfo()
	info.ImageRef = imageRef
	svc := newSvc(t, cfg, &fakeDeploymentStore{info: info})
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
		&recordingDeploymentStore{info: deployableInfo()},
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
	svc := NewService(fakeBackend{}, &recordingDeploymentStore{info: deployableInfo()}, pub, strictCfg(), zap.NewNop())
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
	store := &recordingDeploymentStore{info: deployableInfo()}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	svc := NewService(
		fakeBackend{
			runErr: fmt.Errorf("provider failed\nwith token=redacted?"),
			onRun:  cancelRequest,
		},
		store,
		pub,
		strictCfg(),
		zap.NewNop(),
	)

	_, err := svc.Deploy(requestCtx, req())
	if err == nil {
		t.Fatal("Deploy() error = nil, want backend error")
	}

	assertLogContains(t, pub.lines, "backend run failed: provider failed with token=[redacted]")
	if got, want := strings.Join(store.statuses, ","), "provisioning,building"; got != want {
		t.Fatalf("status updates = %q, want retryable %q", got, want)
	}
	for i, contextErr := range store.statusContextErrs {
		if contextErr != nil {
			t.Fatalf("status update %d inherited cancelled request context: %v", i, contextErr)
		}
	}
}

func TestDeployRollsBackContainerWhenRunningStateCannotPersist(t *testing.T) {
	pub := &recordingPublisher{}
	stopCalls := 0
	var stopContextErr error
	var persistContextErr error
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	store := &recordingDeploymentStore{
		info:                 deployableInfo(),
		setRunningErr:        errors.New("database is busy"),
		setRunningContextErr: &persistContextErr,
	}
	svc := NewService(
		fakeBackend{
			stopCalls:      &stopCalls,
			stopContextErr: &stopContextErr,
			onRun:          cancelRequest,
		},
		store,
		pub,
		strictCfg(),
		zap.NewNop(),
	)

	result, err := svc.Deploy(requestCtx, req())
	if result != nil {
		t.Fatalf("Deploy() result = %#v, want nil", result)
	}
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("Deploy() error = %v, want transient", err)
	}
	if stopCalls != 1 {
		t.Fatalf("backend Stop calls = %d, want 1", stopCalls)
	}
	if persistContextErr != nil {
		t.Fatalf("SetDeployRunning inherited cancelled request context: %v", persistContextErr)
	}
	if stopContextErr != nil {
		t.Fatalf("cleanup inherited cancelled request context: %v", stopContextErr)
	}
	if got, want := strings.Join(store.statuses, ","), "provisioning,building"; got != want {
		t.Fatalf("status updates = %q, want %q", got, want)
	}
	assertLogContains(t, pub.lines, "could not persist the running deployment")
	for _, line := range pub.lines {
		if strings.Contains(line.Text, "public URL ready") {
			t.Fatalf("uncommitted deployment was published as ready: %q", line.Text)
		}
	}
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
		&recordingDeploymentStore{info: deployableInfo()},
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
		&recordingDeploymentStore{info: deployableInfo()},
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

// Stopping must NOT release the image. The image is what lets Start bring the
// deploy back in seconds instead of a rebuild, and releasing it here would
// make every stop — including an unattended TTL expiry — irreversible.
//
// This is the invariant the image sweep is written around: it takes 'failed'
// and 'deleted' and skips 'stopped'. If this test starts failing because an
// image was removed, the restart button has quietly become decoration.
func TestUndeployKeepsTheImageSoTheDeployCanStartAgain(t *testing.T) {
	var removed []string
	store := &recordingDeploymentStore{info: deployableInfo()}
	svc := NewService(
		fakeBackend{removedImages: &removed},
		store,
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	if err := svc.Undeploy(context.Background(), testDeployID, "container-id"); err != nil {
		t.Fatalf("Undeploy() error = %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("stopping removed %v; a stopped deploy has to keep its image", removed)
	}
	if len(store.markedImages) != 0 {
		t.Fatalf("stopping recorded image cleanup that did not happen: %v", store.markedImages)
	}
	if len(store.statuses) != 1 || store.statuses[0] != "stopped" {
		t.Fatalf("statuses = %v, want exactly one transition to stopped", store.statuses)
	}
}

// The marker is written only after Docker confirms the image is gone, so a
// backend failure must not leave a row claiming reclaimed disk that is still
// occupied — that row would never be swept again.
func TestRemoveImageDoesNotRecordCleanupThatDidNotHappen(t *testing.T) {
	store := &recordingDeploymentStore{info: deployableInfo()}
	svc := NewService(
		fakeBackend{removeImageErr: errors.New("image is referenced by a running container")},
		store,
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	if err := svc.RemoveImage(context.Background(), testDeployID, testImageRef); err == nil {
		t.Fatal("RemoveImage reported success while the backend refused")
	}
	if len(store.markedImages) != 0 {
		t.Fatalf("marked %v as cleaned up despite the backend failing", store.markedImages)
	}
}

// A deploy that failed before anything was built has no artifact. Calling the
// daemon for an empty reference would be an error, and recording cleanup would
// be a lie.
func TestRemoveImageIsANoOpWithoutAnImageRef(t *testing.T) {
	var removed []string
	store := &recordingDeploymentStore{info: deployableInfo()}
	svc := NewService(
		fakeBackend{removedImages: &removed},
		store,
		nil,
		strictCfg(),
		zap.NewNop(),
	)

	if err := svc.RemoveImage(context.Background(), testDeployID, "   "); err != nil {
		t.Fatalf("RemoveImage() error = %v", err)
	}
	if len(removed) != 0 || len(store.markedImages) != 0 {
		t.Fatalf("an empty image_ref reached the backend (%v) or the marker (%v)", removed, store.markedImages)
	}
}

func TestUndeployMismatchedContainerRejectedBeforeStop(t *testing.T) {
	calls := 0
	svc := NewService(
		fakeBackend{stopCalls: &calls},
		&recordingDeploymentStore{info: deployableInfo()},
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
		&recordingDeploymentStore{err: deployments.ErrNotFound},
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
		&recordingDeploymentStore{info: info},
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
		&recordingDeploymentStore{info: deployableInfo()},
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
	svc := NewService(fakeBackend{}, &recordingDeploymentStore{info: deployableInfo()}, pub, strictCfg(), zap.NewNop())

	if err := svc.StopExpired(context.Background(), testDeployID, "container-id"); err != nil {
		t.Fatalf("StopExpired() error = %v", err)
	}

	want := []string{
		"watchdog TTL cleanup started",
		"stopping deployment",
		"stopped cleanly, image kept for restart",
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

// assertingDeploymentStore lets a test fail when GetDeploy is unexpectedly called.
type assertingDeploymentStore struct {
	onGet func()
}

func (a assertingDeploymentStore) GetDeploy(context.Context, string) (*deployments.Info, error) {
	if a.onGet != nil {
		a.onGet()
	}
	return nil, errors.New("should not be called")
}
func (a assertingDeploymentStore) UpdateDeployStatus(context.Context, string, string, *string) error {
	panic("not used")
}
func (a assertingDeploymentStore) SetDeployRunning(context.Context, string, deployments.SetRunningRequest) error {
	panic("not used")
}
func (a assertingDeploymentStore) MarkDeployImageDeleted(context.Context, string) error {
	panic("not used")
}
