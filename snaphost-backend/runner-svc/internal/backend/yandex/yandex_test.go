//go:build yandex
// +build yandex

package yandex

import (
	"errors"
	"strings"
	"testing"
	"time"

	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/logs"
)

func TestContainerNameForPrefersSubdomainAndNormalizes(t *testing.T) {
	req := backend.RunRequest{
		DeployID:  "DEPLOY_123",
		Subdomain: "Proj_ABC.Example",
	}

	got := containerNameFor(req)
	if got != "proj-abc-example" {
		t.Fatalf("containerNameFor() = %q, want %q", got, "proj-abc-example")
	}
	if !validYandexName(got) {
		t.Fatalf("containerNameFor() produced invalid Yandex name %q", got)
	}
}

func TestContainerNameForFallsBackToDeployID(t *testing.T) {
	req := backend.RunRequest{DeployID: "123_DEPLOY"}

	got := containerNameFor(req)
	if got != "proj-123-deploy" {
		t.Fatalf("containerNameFor() = %q, want %q", got, "proj-123-deploy")
	}
}

func TestContainerNameForTruncatesToYandexLimit(t *testing.T) {
	req := backend.RunRequest{
		DeployID:  "deploy",
		Subdomain: "proj-abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz",
	}

	got := containerNameFor(req)
	if len(got) > 63 {
		t.Fatalf("containerNameFor() length = %d, want <= 63", len(got))
	}
	if !validYandexName(got) {
		t.Fatalf("containerNameFor() produced invalid Yandex name %q", got)
	}
}

func TestRevisionEnvOmitsReservedPort(t *testing.T) {
	env := revisionEnv(backend.RunRequest{
		Port: 3000,
		Env: map[string]string{
			"NODE_ENV": "production",
			"PORT":     "9999",
		},
	})

	if env["NODE_ENV"] != "production" {
		t.Fatalf("NODE_ENV = %q, want production", env["NODE_ENV"])
	}
	if _, ok := env["PORT"]; ok {
		t.Fatalf("PORT should be omitted because Yandex reserves/injects it")
	}
}

func TestYandexResourceLabels(t *testing.T) {
	labels := yandexResourceLabels(backend.RunRequest{
		DeployID: "B132CB0D-CE92-4009-A8F3-221D86D8607C",
		UserID:   "A4A355F8-9769-454F-B5C0-9782ACCEEBC0",
	})

	if labels["managed_by"] != "snaphost" {
		t.Fatalf("managed_by = %q, want snaphost", labels["managed_by"])
	}
	if labels["deploy_id"] != "b132cb0d-ce92-4009-a8f3-221d86d8607c" {
		t.Fatalf("deploy_id = %q", labels["deploy_id"])
	}
	if labels["user_id"] != "a4a355f8-9769-454f-b5c0-9782acceebc0" {
		t.Fatalf("user_id = %q", labels["user_id"])
	}
}

func TestSanitizeYandexLabelValue(t *testing.T) {
	got := sanitizeYandexLabelValue(" Secret=VALUE \n with spaces and !")
	if got != "secretvaluewithspacesand" {
		t.Fatalf("sanitizeYandexLabelValue() = %q", got)
	}
	if got := sanitizeYandexLabelValue(""); got != "unknown" {
		t.Fatalf("empty sanitizeYandexLabelValue() = %q, want unknown", got)
	}
}

func TestEffectiveTTL(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "positive", in: 45 * time.Second, want: 45 * time.Second},
		{name: "zero", in: 0, want: maxServerlessExecTimeout},
		{name: "too large", in: maxServerlessExecTimeout + time.Second, want: maxServerlessExecTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveTTL(tc.in); got != tc.want {
				t.Fatalf("effectiveTTL(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestMemoryBytesRoundsToYandexMultiple(t *testing.T) {
	b := &YandexBackend{memoryMB: 513}

	const mib = 1024 * 1024
	if got, want := b.memoryBytes(), int64(640*mib); got != want {
		t.Fatalf("memoryBytes() = %d, want %d", got, want)
	}
}

func TestCoreFractionFromLimit(t *testing.T) {
	cases := []struct {
		limit float64
		want  int64
	}{
		{limit: 0, want: 50},
		{limit: 0.03, want: 5},
		{limit: 0.52, want: 55},
		{limit: 2, want: 100},
	}

	for _, tc := range cases {
		if got := coreFractionFromLimit(tc.limit); got != tc.want {
			t.Fatalf("coreFractionFromLimit(%v) = %d, want %d", tc.limit, got, tc.want)
		}
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

func TestPublishStageUsesRequestedStage(t *testing.T) {
	pub := &recordingPublisher{}
	b := &YandexBackend{publisher: pub}

	b.publishStage("deploy-id", "runtime-shutdown", "container delete started", "info")

	if len(pub.lines) != 1 {
		t.Fatalf("published %d lines, want 1", len(pub.lines))
	}
	line := pub.lines[0]
	if line.DeployID != "deploy-id" || line.Stage != "runtime-shutdown" || line.Text != "container delete started" || line.Level != "info" {
		t.Fatalf("unexpected log line: %+v", line)
	}
}

func TestUserVisibleYandexErrorCompactsAndTruncates(t *testing.T) {
	got := userVisibleYandexError(errors.New(strings.Repeat("b", 300) + "\nraw details"))
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("userVisibleYandexError() did not compact newlines: %q", got)
	}
	if len(got) > 243 {
		t.Fatalf("userVisibleYandexError() length = %d, want <= 243", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("userVisibleYandexError() = %q, want truncation suffix", got)
	}
}

func TestUserVisibleYandexErrorRedactsSensitiveFragments(t *testing.T) {
	got := userVisibleYandexError(errors.New("rpc failed token=abc123 Authorization: Bearer very-secret secret: value"))
	if strings.Contains(got, "abc123") || strings.Contains(got, "very-secret") || strings.Contains(got, "value") {
		t.Fatalf("userVisibleYandexError() did not redact sensitive fragments: %q", got)
	}
	if !strings.Contains(got, "token=[redacted]") || !strings.Contains(got, "Authorization=[redacted]") || !strings.Contains(got, "secret=[redacted]") {
		t.Fatalf("userVisibleYandexError() missing redaction markers: %q", got)
	}
}
