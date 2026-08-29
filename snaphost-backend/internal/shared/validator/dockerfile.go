// Package validator provides Dockerfile security and policy validation.
package validator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationIssue represents a single policy violation found in a Dockerfile.
type ValidationIssue struct {
	// Line is the 1-based line number where the issue was found.
	Line int `json:"line"`
	// Code is a machine-readable issue code.
	Code string `json:"code"`
	// Message is a human-readable description of the issue.
	Message string `json:"message"`
}

// forbiddenRunSubstrings are shell patterns banned from RUN instructions.
var forbiddenRunSubstrings = []string{
	"/var/run/docker.sock",
	"docker.sock",
	"--privileged",
	"chmod 777 /",
	" mount ",
	"/proc/1/",
	"/sys/",
}

// secretArgPattern matches build arg names that likely contain secrets.
var secretArgPattern = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|KEY|CREDENTIAL)`)

// curlPipePattern matches insecure install patterns: curl ... | sh, wget ... | sh.
var curlPipePattern = regexp.MustCompile(`(?i)(curl|wget)\s+.*\|\s*(sh|bash|zsh)`)

// Mode controls the strictness of base image validation.
type Mode int

const (
	// ModeStrict applies the narrow allow-list intended for LLM-generated
	// Dockerfiles. Use when we authored the Dockerfile.
	ModeStrict Mode = iota

	// ModePermissive applies a wider allow-list intended for user-authored
	// Dockerfiles. Use when the Dockerfile shipped with the repo.
	ModePermissive
)

// Options bundles per-validation policy choices.
type Options struct {
	Mode                               Mode
	AllowedBaseImagePrefixesStrict     []string
	AllowedBaseImagePrefixesPermissive []string
}

// ValidateDockerfile is the legacy entry point: strict mode only.
// Kept for backward compatibility — new callers should use
// ValidateDockerfileWithOptions.
func ValidateDockerfile(path string, allowedBaseImagePrefixes []string) ([]ValidationIssue, error) {
	return ValidateDockerfileWithOptions(path, Options{
		Mode:                           ModeStrict,
		AllowedBaseImagePrefixesStrict: allowedBaseImagePrefixes,
	})
}

// ValidateDockerfileWithOptions is the mode-aware validator. It checks a
// Dockerfile for security and policy violations under the chosen mode's
// allow-list, plus tag-policy rules that apply regardless of mode.
// Returns a slice of issues (empty means pass). Returns an error only on
// file read/parse failures.
func ValidateDockerfileWithOptions(path string, opts Options) ([]ValidationIssue, error) {
	var activeList []string
	switch opts.Mode {
	case ModePermissive:
		activeList = opts.AllowedBaseImagePrefixesPermissive
		if len(activeList) == 0 {
			activeList = opts.AllowedBaseImagePrefixesStrict
		}
	default: // ModeStrict
		activeList = opts.AllowedBaseImagePrefixesStrict
	}

	// Canonicalize the allow-list once so prefix match below is
	// independent of how users wrote the entries in env (short "node:" vs
	// fully-qualified "docker.io/library/node:"). See normalize.go.
	normalizedAllowList := make([]string, len(activeList))
	for i, p := range activeList {
		normalizedAllowList[i] = normalizeAllowedPrefix(p)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dockerfile: %w", err)
	}
	defer f.Close()

	var issues []ValidationIssue
	var lastFromImage string
	lineNum := 0
	inMultiLineRun := false
	var multiLineRunStart int
	var multiLineRunBuf strings.Builder

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)

		// Handle multi-line RUN continuation.
		if inMultiLineRun {
			multiLineRunBuf.WriteString(" ")
			multiLineRunBuf.WriteString(trimmed)
			if !strings.HasSuffix(trimmed, "\\") {
				inMultiLineRun = false
				issues = append(issues, checkRunContent(multiLineRunStart, multiLineRunBuf.String())...)
			}
			continue
		}

		// Skip comments and blank lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		upper := strings.ToUpper(trimmed)

		// FROM — base image check.
		if strings.HasPrefix(upper, "FROM ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				image := parts[1]
				lastFromImage = image
				if !isAllowedBaseImage(image, normalizedAllowList) {
					issues = append(issues, ValidationIssue{
						Line:    lineNum,
						Code:    "base_image_not_allowed",
						Message: fmt.Sprintf("base image %q is not in the allowed list", image),
					})
				}
				// Tag policy applies regardless of mode.
				if tagIssue := checkBaseImageTag(image, lineNum); tagIssue != nil {
					issues = append(issues, *tagIssue)
				}
			}
		}

		// USER root or USER 0 in any stage (we check final stage at the end).
		if strings.HasPrefix(upper, "USER ") {
			userVal := strings.TrimSpace(strings.TrimPrefix(upper, "USER "))
			_ = lastFromImage // Track that we've seen the latest FROM.
			if userVal == "ROOT" || userVal == "0" {
				issues = append(issues, ValidationIssue{
					Line:    lineNum,
					Code:    "user_root",
					Message: "running as root user is not allowed",
				})
			}
		}

		// ADD with URL source.
		if strings.HasPrefix(upper, "ADD ") {
			parts := strings.Fields(trimmed)
			for _, p := range parts[1:] {
				lp := strings.ToLower(p)
				if strings.HasPrefix(lp, "http://") || strings.HasPrefix(lp, "https://") {
					issues = append(issues, ValidationIssue{
						Line:    lineNum,
						Code:    "add_url_source",
						Message: "ADD with URL source is not allowed; use COPY + RUN curl instead",
					})
					break
				}
			}
		}

		// RUN instruction checks.
		if strings.HasPrefix(upper, "RUN ") {
			content := strings.TrimPrefix(trimmed, trimmed[:4]) // preserve original case
			if strings.HasSuffix(content, "\\") {
				inMultiLineRun = true
				multiLineRunStart = lineNum
				multiLineRunBuf.Reset()
				multiLineRunBuf.WriteString(content)
			} else {
				issues = append(issues, checkRunContent(lineNum, content)...)
			}
		}

		// VOLUME with host paths.
		if strings.HasPrefix(upper, "VOLUME ") {
			volContent := strings.TrimPrefix(trimmed, trimmed[:7])
			for _, forbidden := range []string{"/var", "/etc", "/root"} {
				if strings.Contains(volContent, forbidden) {
					issues = append(issues, ValidationIssue{
						Line:    lineNum,
						Code:    "volume_host_path",
						Message: fmt.Sprintf("VOLUME referencing host path %q is not allowed", forbidden),
					})
				}
			}
		}

		// ARG — secret name detection (warn-level, not blocking).
		if strings.HasPrefix(upper, "ARG ") {
			argName := strings.Fields(trimmed)[1]
			if idx := strings.Index(argName, "="); idx > 0 {
				argName = argName[:idx]
			}
			if secretArgPattern.MatchString(argName) {
				issues = append(issues, ValidationIssue{
					Line:    lineNum,
					Code:    "secret_build_arg",
					Message: fmt.Sprintf("build arg %q looks like a secret — avoid embedding secrets in images", argName),
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dockerfile: %w", err)
	}

	return issues, nil
}

// checkRunContent validates the content of a RUN instruction.
func checkRunContent(line int, content string) []ValidationIssue {
	var issues []ValidationIssue
	lower := strings.ToLower(content)

	for _, substr := range forbiddenRunSubstrings {
		if strings.Contains(lower, strings.ToLower(substr)) {
			issues = append(issues, ValidationIssue{
				Line:    line,
				Code:    "forbidden_run_command",
				Message: fmt.Sprintf("RUN contains forbidden pattern %q", substr),
			})
		}
	}

	if curlPipePattern.MatchString(content) {
		issues = append(issues, ValidationIssue{
			Line:    line,
			Code:    "insecure_install",
			Message: "piping curl/wget to shell is an insecure install pattern",
		})
	}

	return issues
}

// ValidateCopySources walks the Dockerfile and verifies that every local source
// referenced by COPY / ADD exists in the build context. Catches the common AI
// failure mode of hallucinating support files like nginx.conf.
//
// Skipped: --from=<stage> COPY (multi-stage), URL sources, and paths that
// contain unexpanded build-arg references like ${VERSION}.
func ValidateCopySources(dockerfilePath, contextDir string) ([]ValidationIssue, error) {
	f, err := os.Open(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("open dockerfile: %w", err)
	}
	defer f.Close()

	var issues []ValidationIssue
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "COPY ") && !strings.HasPrefix(upper, "ADD ") {
			continue
		}

		tokens := strings.Fields(trimmed)[1:]
		var nonFlag []string
		fromAnotherStage := false
		for _, t := range tokens {
			if strings.HasPrefix(t, "--from=") {
				fromAnotherStage = true
				continue
			}
			if strings.HasPrefix(t, "--") {
				continue
			}
			nonFlag = append(nonFlag, t)
		}
		if fromAnotherStage || len(nonFlag) < 2 {
			continue
		}

		sources := nonFlag[:len(nonFlag)-1]
		for _, src := range sources {
			src = strings.Trim(src, `"',[]`)
			if src == "" {
				continue
			}
			lower := strings.ToLower(src)
			if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
				continue
			}
			if strings.Contains(src, "${") || strings.Contains(src, "$(") {
				continue
			}

			// Glob sources are optional in Docker semantics: a COPY succeeds as
			// long as at least one source on the line matches. We can't decide
			// that statically without parsing every source on the line, so we
			// just skip glob checks. The check exists to catch hallucinated
			// literal filenames (e.g. nginx.conf), not optional lockfiles.
			if strings.ContainsAny(src, "*?[") {
				continue
			}

			cleaned := strings.TrimPrefix(strings.TrimPrefix(src, "./"), "/")
			full := filepath.Join(contextDir, cleaned)

			if _, statErr := os.Stat(full); statErr == nil {
				continue
			}

			issues = append(issues, ValidationIssue{
				Line:    lineNum,
				Code:    "copy_source_missing",
				Message: fmt.Sprintf("COPY/ADD source %q does not exist in the repository", src),
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dockerfile: %w", err)
	}
	return issues, nil
}

// checkBaseImageTag enforces explicit, non-:latest tags. Returns nil if the
// image is acceptable. Returns an issue for:
//
//   - missing tag entirely (FROM "node")
//   - explicit :latest (FROM "node:latest")
//
// Digest-pinned images (FROM "node@sha256:...") are accepted as if they had
// an explicit tag — the digest is stronger than a tag.
//
// Multi-arch tag refs like "node:20-alpine" pass. "scratch" is treated as a
// special pre-pinned image and passes (no tag possible, well-known semantics).
func checkBaseImageTag(image string, line int) *ValidationIssue {
	if strings.EqualFold(image, "scratch") {
		return nil
	}
	if strings.Contains(image, "@sha256:") {
		return nil
	}
	// Image refs can contain colons in the registry host (e.g.
	// "host.docker.internal:5000/foo"), so look at the LAST path segment.
	lastSlash := strings.LastIndex(image, "/")
	var lastSegment string
	if lastSlash == -1 {
		lastSegment = image
	} else {
		lastSegment = image[lastSlash+1:]
	}
	colonIdx := strings.Index(lastSegment, ":")
	if colonIdx == -1 {
		return &ValidationIssue{
			Line: line,
			Code: "base_image_missing_tag",
			Message: fmt.Sprintf(
				"base image %q has no explicit tag; pin a specific version (e.g. %q)",
				image, image+":1.2.3"),
		}
	}
	tag := lastSegment[colonIdx+1:]
	if strings.EqualFold(tag, "latest") {
		return &ValidationIssue{
			Line: line,
			Code: "base_image_latest_tag",
			Message: fmt.Sprintf(
				"base image %q uses :latest; pin a specific version for reproducible builds",
				image),
		}
	}
	return nil
}

// isAllowedBaseImage checks if the image matches any of the allowed prefixes.
// The normalizedAllowedPrefixes parameter must be pre-normalized via
// normalizeAllowedPrefix — callers are responsible for this precondition.
func isAllowedBaseImage(image string, normalizedAllowedPrefixes []string) bool {
	canonical := strings.ToLower(normalizeImageRef(image))
	for _, prefix := range normalizedAllowedPrefixes {
		// prefix is pre-normalized + lowercased by normalizeAllowedPrefix.
		if strings.HasPrefix(canonical, prefix) {
			return true
		}
	}
	return false
}
