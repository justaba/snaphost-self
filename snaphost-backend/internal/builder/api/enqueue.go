package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/internal/builder/clone"
	"snaphost/internal/builder/config"
	"snaphost/internal/builder/queue"
)

// buildArgKeyPattern bounds build-arg names to what a Dockerfile ARG accepts,
// so a key cannot smuggle shell syntax into the build.
var buildArgKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// RequestError is a rejected build request, carrying the HTTP status the API
// answers with. The status is part of the contract rather than presentation:
// the saga reads it to tell "this will never work" from "try again", and the
// in-process caller maps it to the same distinction without a round trip.
type RequestError struct {
	Status  int
	Code    string
	Message string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func reject(status int, code, message string) error {
	return &RequestError{Status: status, Code: code, Message: message}
}

// Enqueuer validates a build request and puts it on the queue.
//
// It exists so there is exactly one validation path. The HTTP handler is a
// shell around it, and so is the saga's in-process client — a second copy of
// the source matrix below would drift, and the half that drifted would be the
// one nobody was testing.
type Enqueuer struct {
	Queue *queue.Queue
	Cfg   *config.Config
	Log   *zap.Logger
}

// Enqueue validates req and returns the queued job id. Every rejection is a
// *RequestError; anything else is an infrastructure failure worth retrying.
func (e *Enqueuer) Enqueue(ctx context.Context, req BuildRequest) (string, error) {
	if _, err := uuid.Parse(req.DeployID); err != nil {
		return "", reject(http.StatusBadRequest, "invalid_deploy_id", "deploy_id must be a valid UUID")
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		return "", reject(http.StatusBadRequest, "invalid_user_id", "user_id must be a valid UUID")
	}

	// Normalize and validate the source matrix: git_* requires
	// repo_url+branch and no upload_id; archive requires upload_id and
	// no repo_url/branch.
	if req.SourceType == "" {
		req.SourceType = queue.SourceGitPublic
	}
	switch req.SourceType {
	case queue.SourceGitPublic, queue.SourceGitPrivate:
		if req.UploadID != "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "upload_id is only valid for source_type=archive")
		}
		if req.RepoURL == "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "repo_url is required for git sources")
		}
		if req.Branch == "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "branch is required for git sources")
		}
		if req.SourceType == queue.SourceGitPrivate {
			if req.CredentialID == "" {
				return "", reject(http.StatusBadRequest, "invalid_body", "credential_id is required for source_type=git_private")
			}
			if _, err := uuid.Parse(req.CredentialID); err != nil {
				return "", reject(http.StatusBadRequest, "invalid_credential_id", "credential_id must be a valid UUID")
			}
		} else if req.CredentialID != "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "credential_id is only valid for source_type=git_private")
		}
	case queue.SourceArchive:
		if req.RepoURL != "" || req.Branch != "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "repo_url and branch are not valid for source_type=archive")
		}
		if req.CredentialID != "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "credential_id is only valid for source_type=git_private")
		}
		if req.UploadID == "" {
			return "", reject(http.StatusBadRequest, "invalid_body", "upload_id is required for source_type=archive")
		}
		if _, err := uuid.Parse(req.UploadID); err != nil {
			return "", reject(http.StatusBadRequest, "invalid_upload_id", "upload_id must be a valid UUID")
		}
	default:
		return "", reject(http.StatusBadRequest, "invalid_source_type",
			"source_type must be one of git_public, git_private, archive")
	}

	if len(req.Branch) > 100 {
		return "", reject(http.StatusBadRequest, "invalid_branch", "branch name must be 100 characters or fewer")
	}

	if len(req.BuildArgs) > 20 {
		return "", reject(http.StatusBadRequest, "too_many_build_args", "maximum 20 build args allowed")
	}
	for key := range req.BuildArgs {
		if !buildArgKeyPattern.MatchString(key) {
			return "", reject(http.StatusBadRequest, "invalid_build_arg_key",
				"build arg keys must match [A-Z_][A-Z0-9_]{0,63}: "+key)
		}
	}

	// Syntactic check only at enqueue time. Full validation (DNS + IP filter
	// + IP pinning) lives in the worker, immediately before clone — see
	// clone.ValidateRepoURL caller in pipeline/runner.go. TOCTOU window
	// is microseconds, not minutes. Archive sources have no URL to check.
	if req.SourceType != queue.SourceArchive {
		if err := clone.ValidateRepoURLSyntactic(req.RepoURL); err != nil {
			return "", reject(http.StatusBadRequest, "invalid_repo_url", err.Error())
		}
	}

	active, err := e.Queue.CountActive(ctx, req.UserID)
	if err != nil {
		e.Log.Error("failed to check active builds", zap.Error(err))
		return "", fmt.Errorf("count active builds: %w", err)
	}
	if active >= e.Cfg.MaxConcurrentBuildsPerUser {
		// 429 rather than 400: waiting is exactly what helps here, so the
		// saga must retry this rather than compensate.
		return "", reject(http.StatusTooManyRequests, "concurrency_limit",
			"maximum concurrent builds reached — wait for a build to complete")
	}

	jobID, err := e.Queue.Enqueue(ctx, queue.Job{
		DeployID:     req.DeployID,
		UserID:       req.UserID,
		SourceType:   req.SourceType,
		RepoURL:      req.RepoURL,
		Branch:       req.Branch,
		UploadID:     req.UploadID,
		CredentialID: req.CredentialID,
		BuildArgs:    req.BuildArgs,
		QueuedAt:     time.Now().UTC(),
	})
	if err != nil {
		e.Log.Error("failed to enqueue build job", zap.Error(err))
		return "", fmt.Errorf("enqueue build job: %w", err)
	}
	return jobID, nil
}
