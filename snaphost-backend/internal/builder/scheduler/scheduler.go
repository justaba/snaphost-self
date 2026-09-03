// Package scheduler validates build requests and submits them to the local
// build queue.
package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/internal/builder/clone"
	"snaphost/internal/builder/config"
	"snaphost/internal/builder/queue"
)

var buildArgKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

type BuildRequest struct {
	DeployID     string
	UserID       string
	SourceType   string
	RepoURL      string
	Branch       string
	UploadID     string
	CredentialID string
	BuildArgs    map[string]string
}

type RequestError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *RequestError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func reject(code, message string) error {
	return &RequestError{Code: code, Message: message}
}

type Scheduler struct {
	Queue *queue.Queue
	Cfg   *config.Config
	Log   *zap.Logger
}

func (s *Scheduler) Enqueue(ctx context.Context, req BuildRequest) (string, error) {
	if _, err := uuid.Parse(req.DeployID); err != nil {
		return "", reject("invalid_deploy_id", "deploy_id must be a valid UUID")
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		return "", reject("invalid_user_id", "user_id must be a valid UUID")
	}

	if req.SourceType == "" {
		req.SourceType = queue.SourceGitPublic
	}
	switch req.SourceType {
	case queue.SourceGitPublic, queue.SourceGitPrivate:
		if req.UploadID != "" {
			return "", reject("invalid_body", "upload_id is only valid for source_type=archive")
		}
		if req.RepoURL == "" {
			return "", reject("invalid_body", "repo_url is required for git sources")
		}
		if req.Branch == "" {
			return "", reject("invalid_body", "branch is required for git sources")
		}
		if req.SourceType == queue.SourceGitPrivate {
			if req.CredentialID == "" {
				return "", reject("invalid_body", "credential_id is required for source_type=git_private")
			}
			if _, err := uuid.Parse(req.CredentialID); err != nil {
				return "", reject("invalid_credential_id", "credential_id must be a valid UUID")
			}
		} else if req.CredentialID != "" {
			return "", reject("invalid_body", "credential_id is only valid for source_type=git_private")
		}
	case queue.SourceArchive:
		if req.RepoURL != "" || req.Branch != "" {
			return "", reject("invalid_body", "repo_url and branch are not valid for source_type=archive")
		}
		if req.CredentialID != "" {
			return "", reject("invalid_body", "credential_id is only valid for source_type=git_private")
		}
		if req.UploadID == "" {
			return "", reject("invalid_body", "upload_id is required for source_type=archive")
		}
		if _, err := uuid.Parse(req.UploadID); err != nil {
			return "", reject("invalid_upload_id", "upload_id must be a valid UUID")
		}
	default:
		return "", reject("invalid_source_type", "source_type must be one of git_public, git_private, archive")
	}

	if len(req.Branch) > 100 {
		return "", reject("invalid_branch", "branch name must be 100 characters or fewer")
	}
	if len(req.BuildArgs) > 20 {
		return "", reject("too_many_build_args", "maximum 20 build args allowed")
	}
	for key := range req.BuildArgs {
		if !buildArgKeyPattern.MatchString(key) {
			return "", reject("invalid_build_arg_key", "build arg keys must match [A-Z_][A-Z0-9_]{0,63}: "+key)
		}
	}

	if req.SourceType != queue.SourceArchive {
		if err := clone.ValidateRepoURLSyntactic(req.RepoURL); err != nil {
			return "", reject("invalid_repo_url", err.Error())
		}
	}

	active, err := s.Queue.CountActive(ctx, req.UserID)
	if err != nil {
		s.Log.Error("failed to check active builds", zap.Error(err))
		return "", fmt.Errorf("count active builds: %w", err)
	}
	if active >= s.Cfg.MaxConcurrentBuildsPerUser {
		return "", &RequestError{
			Code:      "concurrency_limit",
			Message:   "maximum concurrent builds reached — wait for a build to complete",
			Retryable: true,
		}
	}

	jobID, err := s.Queue.Enqueue(ctx, queue.Job{
		DeployID: req.DeployID, UserID: req.UserID, SourceType: req.SourceType,
		RepoURL: req.RepoURL, Branch: req.Branch, UploadID: req.UploadID,
		CredentialID: req.CredentialID, BuildArgs: req.BuildArgs, QueuedAt: time.Now().UTC(),
	})
	if err != nil {
		s.Log.Error("failed to enqueue build job", zap.Error(err))
		return "", fmt.Errorf("enqueue build job: %w", err)
	}
	return jobID, nil
}
