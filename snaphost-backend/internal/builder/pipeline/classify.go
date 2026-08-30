package pipeline

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"snaphost/internal/builder/ai"
	"snaphost/internal/builder/clone"
)

// classifyValidationError wraps the error returned by clone.ValidateRepoURL
// (or ValidateRepoURLSyntactic) with the appropriate transient/permanent tag.
// ErrResolverFailure is the only transient case — DNS resolver itself
// down is plausibly temporary. Filter rejections, host-not-in-allowlist,
// and syntactic problems all stay permanent.
func classifyValidationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, clone.ErrResolverFailure) {
		return Transient(err)
	}
	return Permanent(err)
}

// classifyGitError classifies an error returned by go-git's clone or
// related transport calls. Known sentinels and our own pinned-dialer
// rejection are permanent; net.Error timeouts and connection refused are
// transient; everything unknown defaults to permanent (conservative —
// retry only when we have positive evidence it would help).
func classifyGitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, transport.ErrRepositoryNotFound) ||
		errors.Is(err, transport.ErrAuthenticationRequired) {
		return Permanent(err)
	}
	// Pinned-dialer rejection — our Task 4 defence, string-matched because
	// the error is constructed with fmt.Errorf and not exported as a sentinel.
	if strings.Contains(err.Error(), "unexpected host") {
		return Permanent(err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return Transient(err)
	}
	// Connection refused / DNS / TCP reset errors arrive as *net.OpError —
	// not Timeout but transient in nature.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return Transient(err)
	}
	return Permanent(err)
}

// classifyAIError classifies errors returned by ai.Client.GenerateDockerfile.
// ErrAIUnavailable → transient (upstream / network); ErrAIRefused → permanent.
// For 4xx non-422 (covered by ErrAIRefused) it emits a WARN log because
// such codes may indicate a builder-svc bug (auth misconfigured, wrong
// endpoint, malformed payload) rather than legitimate AI refusal.
func classifyAIError(err error, log *zap.Logger) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ai.ErrAIUnavailable) {
		return Transient(err)
	}
	if errors.Is(err, ai.ErrAIRefused) {
		// Heuristic: if the underlying error text mentions a 4xx status
		// code other than 422, this is more likely a builder-svc bug.
		msg := err.Error()
		if strings.Contains(msg, "status 400") ||
			strings.Contains(msg, "status 401") ||
			strings.Contains(msg, "status 403") ||
			strings.Contains(msg, "status 404") {
			log.Warn("AI returned 4xx — may indicate builder-svc bug rather than legitimate AI refusal",
				zap.Error(err))
		}
		return Permanent(err)
	}
	// Unknown error from AI client — default permanent to avoid retry
	// loops on contract changes we missed.
	return Permanent(err)
}

// classifyBuildKitError classifies errors returned by Builder.Build. gRPC
// codes.Unavailable is transient (buildkitd down / restarting). All other
// solve errors — Dockerfile parse failures, RUN exit non-zero, layer pull
// auth — are permanent. BuildKit returns opaque error messages for many
// of these; we don't substring-match because the false-positive risk is
// higher than the false-negative cost (extra retry on a permanent failure).
//
// TODO(observability): BuildKit OOM cases arrive as opaque "context deadline
// exceeded" or unspecified errors. They are currently classified permanent.
// If we see them recurring in prod logs, add specific detection.
func classifyBuildKitError(err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
		return Transient(err)
	}
	return Permanent(summariseSolveError(err))
}

// solveProcessError matches the shape BuildKit uses when a RUN step exits
// non-zero. The command is captured non-greedily so a Dockerfile with several
// quoted strings does not swallow the rest of the message.
var solveProcessError = regexp.MustCompile(`(?s)failed to solve: process ".*?" did not complete successfully: exit code: (\d+)`)

// summariseSolveError replaces BuildKit's process-failure text with something
// an operator can read.
//
// The original embeds the entire shell command, which for a generated
// Dockerfile is a three-hundred-character if/elif chain, and then ends with
// "exit code: 1". None of that says what went wrong — the reason is in the
// step's output, which is already streamed to the build log and archived with
// the deploy. So the summary keeps the exit code, which is the only fact the
// message actually carried, and says where to look for the rest.
//
// Anything that is not a process failure is returned unchanged: BuildKit's
// other messages (a Dockerfile that will not parse, a base image that cannot
// be pulled) name their own problem.
func summariseSolveError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	match := solveProcessError.FindStringSubmatch(msg)
	if match == nil {
		return err
	}
	return fmt.Errorf("a build step exited with code %s; its output is in the build log", match[1])
}
