// Package scan provides container image vulnerability scanning via the Trivy CLI.
// Scanning is best-effort — if the trivy binary is not available, the scan
// returns a note and nil error so the pipeline is not blocked in dev.
package scan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/builder/logs"
)

// ErrCriticalVulnerability is returned when the scan finds CRITICAL-severity
// vulnerabilities, indicating the image should not be deployed.
var ErrCriticalVulnerability = errors.New("critical vulnerabilities found")

// ScanResult holds the outcome of a Trivy vulnerability scan.
type ScanResult struct {
	// Summary maps severity level to vulnerability count.
	Summary map[string]int `json:"summary"`
	// CriticalCount is the number of CRITICAL-severity vulnerabilities.
	CriticalCount int `json:"critical_count"`
	// HighCount is the number of HIGH-severity vulnerabilities.
	HighCount int `json:"high_count"`
	// RawJSON is the raw Trivy JSON output for archival/debugging.
	RawJSON json.RawMessage `json:"raw_json,omitempty"`
	// Note is a human-readable message (e.g. "trivy not available").
	Note string `json:"note,omitempty"`
}

// trivyResults is a minimal representation of Trivy's JSON output.
type trivyResults struct {
	Results []struct {
		Vulnerabilities []struct {
			Severity string `json:"Severity"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// Scanner provides container image vulnerability scanning.
type Scanner struct {
	pub              logs.Publisher
	log              *zap.Logger
	registryInsecure bool
	registryHost     string
	credentials      CredentialsProvider
}

// CredentialsProvider returns short-lived credentials for the configured
// target registry. Implementations must not log returned values.
type CredentialsProvider func(context.Context) (username, password string, err error)

// NewScanner creates a new Scanner instance. When registryInsecure is true,
// Trivy will skip TLS certificate verification (--insecure flag).
func NewScanner(pub logs.Publisher, log *zap.Logger, registryInsecure bool, registryHost string, credentials CredentialsProvider) *Scanner {
	return &Scanner{
		pub:              pub,
		log:              log,
		registryInsecure: registryInsecure,
		registryHost:     registryHost,
		credentials:      credentials,
	}
}

// Scan runs Trivy against the given image reference and returns the results.
// If the trivy binary is not found, returns a result with a note and nil error.
// If CRITICAL vulnerabilities are found, returns the result AND ErrCriticalVulnerability.
func (s *Scanner) Scan(ctx context.Context, imageRef string) (*ScanResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	_ = s.pub.Publish("", logs.LogLine{
		Stage:     "scan",
		Text:      fmt.Sprintf("scanning image %s for vulnerabilities", imageRef),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	args := s.buildArgs(imageRef)
	cmd := exec.CommandContext(scanCtx, "trivy", args...)
	cmd.Env = os.Environ()

	cleanupAuth, err := s.configureRegistryAuth(scanCtx, cmd, imageRef)
	if err != nil {
		return nil, fmt.Errorf("configure registry auth: %w", err)
	}
	defer cleanupAuth()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		// Check if trivy binary is missing.
		if errors.Is(err, exec.ErrNotFound) {
			s.log.Warn("trivy binary not found — skipping vulnerability scan",
				zap.String("stage", "scan"),
			)
			return &ScanResult{
				Summary: map[string]int{},
				Note:    "trivy not available — vulnerability scan skipped",
			}, nil
		}
		return nil, fmt.Errorf("run trivy: %w (stderr: %s)", err, stderr.String())
	}

	result := &ScanResult{
		Summary: map[string]int{},
		RawJSON: json.RawMessage(stdout.Bytes()),
	}

	var output trivyResults
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		s.log.Warn("failed to parse trivy output",
			zap.String("stage", "scan"),
			zap.Error(err),
		)
		return result, nil
	}

	for _, r := range output.Results {
		for _, v := range r.Vulnerabilities {
			result.Summary[v.Severity]++
		}
	}

	result.CriticalCount = result.Summary["CRITICAL"]
	result.HighCount = result.Summary["HIGH"]

	_ = s.pub.Publish("", logs.LogLine{
		Stage:     "scan",
		Text:      fmt.Sprintf("scan complete: CRITICAL=%d HIGH=%d", result.CriticalCount, result.HighCount),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	if result.CriticalCount > 0 {
		return result, ErrCriticalVulnerability
	}

	return result, nil
}

// buildArgs constructs the Trivy CLI arguments for an image scan. The
// --insecure flag is included only when s.registryInsecure is true
// (Task 7), so Trivy will skip TLS verification when pulling from a
// dev/self-signed registry.
func (s *Scanner) buildArgs(imageRef string) []string {
	args := []string{
		"image",
		"--image-src", "remote",
		"--format", "json",
		"--severity", "CRITICAL,HIGH",
		"--exit-code", "0",
		"--no-progress",
		"--timeout", "2m",
	}
	if s.registryInsecure {
		args = append(args, "--insecure")
	}
	args = append(args, imageRef)
	return args
}

func (s *Scanner) configureRegistryAuth(ctx context.Context, cmd *exec.Cmd, imageRef string) (func(), error) {
	if s.credentials == nil {
		return func() {}, nil
	}
	if s.registryHost == "" || imageRegistryHost(imageRef) != s.registryHost {
		return nil, errors.New("image host does not match configured registry")
	}

	username, password, err := s.credentials(ctx)
	if err != nil {
		return nil, err
	}
	if username == "" || password == "" {
		return nil, errors.New("registry credentials are empty")
	}

	dir, err := os.MkdirTemp("", "snaphost-trivy-auth-")
	if err != nil {
		return nil, fmt.Errorf("create temporary auth directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	config := struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}{Auths: map[string]struct {
		Auth string `json:"auth"`
	}{s.registryHost: {Auth: auth}}}
	raw, err := json.Marshal(config)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("encode registry auth: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write temporary registry auth: %w", err)
	}

	cmd.Env = replaceEnv(cmd.Env, "DOCKER_CONFIG", dir)
	return cleanup, nil
}

func imageRegistryHost(imageRef string) string {
	first, _, ok := strings.Cut(strings.TrimSpace(imageRef), "/")
	if !ok || (!strings.Contains(first, ".") && !strings.Contains(first, ":") && first != "localhost") {
		return "registry-1.docker.io"
	}
	return first
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
