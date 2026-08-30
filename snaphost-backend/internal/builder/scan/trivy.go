// Package scan provides container image vulnerability scanning via the Trivy CLI.
// Scanning is best-effort — if the trivy binary is not available, the scan
// returns a note and nil error so the pipeline is not blocked in dev.
package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	pub logs.Publisher
	log *zap.Logger
}

// NewScanner creates a new Scanner instance.
//
// The registry host, the insecure-TLS flag and the credentials provider went
// with the registry: the image being scanned is in the local Docker daemon,
// put there by the build, so there is nothing to authenticate to and no
// certificate to distrust.
func NewScanner(pub logs.Publisher, log *zap.Logger) *Scanner {
	return &Scanner{
		pub: pub,
		log: log,
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

	cleanupAuth, err := noAuthCleanup()
	if err != nil {
		return nil, fmt.Errorf("prepare scan environment: %w", err)
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

// buildArgs constructs the Trivy CLI arguments for an image scan.
//
// --image-src is "docker" rather than "remote": the image is in the local
// daemon's store because the build loaded it there, and asking Trivy to
// fetch it from a registry would send it looking for something that was never
// pushed. --insecure went with the same change: there is no TLS to skip when
// nothing crosses a network.
func (s *Scanner) buildArgs(imageRef string) []string {
	return []string{
		"image",
		"--image-src", "docker",
		"--format", "json",
		"--severity", "CRITICAL,HIGH",
		"--exit-code", "0",
		"--no-progress",
		"--timeout", "2m",
		imageRef,
	}
}

// noAuthCleanup keeps Scan's shape while there is nothing to clean up.
func noAuthCleanup() (func(), error) { return func() {}, nil }
