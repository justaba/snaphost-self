// Package api provides HTTP handlers for the builder-svc API process.
package api

import (
	"crypto/subtle"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/builder-svc/config"
	"snaphost/builder-svc/internal/clone"
	"snaphost/builder-svc/internal/queue"
)

// Handler provides HTTP endpoints for the builder-svc API.
type Handler struct {
	// Queue is the Redis Streams job queue.
	Queue *queue.Queue
	// Cfg is the service configuration.
	Cfg *config.Config
	// Log is the structured logger.
	Log *zap.Logger
}

// NewHandler creates a new API handler.
func NewHandler(q *queue.Queue, cfg *config.Config, log *zap.Logger) *Handler {
	return &Handler{Queue: q, Cfg: cfg, Log: log}
}

// BuildRequest is the JSON body for the POST /api/v1/build endpoint.
// The per-source field matrix (git_* → repo_url+branch, archive →
// upload_id) is enforced in Build; binding only checks the ids.
type BuildRequest struct {
	// DeployID is the unique deployment identifier (UUID).
	DeployID string `json:"deploy_id" binding:"required"`
	// UserID is the requesting user's identifier (UUID).
	UserID string `json:"user_id" binding:"required"`
	// SourceType is the deploy source; empty defaults to git_public.
	SourceType string `json:"source_type"`
	// RepoURL is the HTTPS Git repository URL. Required for git sources.
	RepoURL string `json:"repo_url"`
	// Branch is the Git branch to build. Required for git sources.
	Branch string `json:"branch"`
	// UploadID references the uploaded archive blob. Required for archive.
	UploadID string `json:"upload_id"`
	// CredentialID references the short-lived git credential blob.
	// Required for git_private; never the secret itself.
	CredentialID string `json:"credential_id"`
	// BuildArgs is an optional map of Docker build arguments.
	BuildArgs map[string]string `json:"build_args,omitempty"`
}

// buildArgKeyPattern validates build arg key format: [A-Z_][A-Z0-9_]{0,63}
var buildArgKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// Build handles POST /api/v1/build — validates the request, checks concurrency
// limits, and enqueues the build job.
func (h *Handler) Build(c *gin.Context) {
	// Webhook secret check (constant-time comparison).
	provided := c.GetHeader("X-Webhook-Secret")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.Cfg.WebhookSecret)) != 1 {
		c.JSON(http.StatusUnauthorized, errResponse("unauthorized", "invalid webhook secret"))
		return
	}

	var req BuildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	// Validate DeployID is a valid UUID.
	if _, err := uuid.Parse(req.DeployID); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "deploy_id must be a valid UUID"))
		return
	}

	// Validate UserID is a valid UUID.
	if _, err := uuid.Parse(req.UserID); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_user_id", "user_id must be a valid UUID"))
		return
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
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "upload_id is only valid for source_type=archive"))
			return
		}
		if req.RepoURL == "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "repo_url is required for git sources"))
			return
		}
		if req.Branch == "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "branch is required for git sources"))
			return
		}
		if req.SourceType == queue.SourceGitPrivate {
			if req.CredentialID == "" {
				c.JSON(http.StatusBadRequest, errResponse("invalid_body", "credential_id is required for source_type=git_private"))
				return
			}
			if _, err := uuid.Parse(req.CredentialID); err != nil {
				c.JSON(http.StatusBadRequest, errResponse("invalid_credential_id", "credential_id must be a valid UUID"))
				return
			}
		} else if req.CredentialID != "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "credential_id is only valid for source_type=git_private"))
			return
		}
	case queue.SourceArchive:
		if req.RepoURL != "" || req.Branch != "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "repo_url and branch are not valid for source_type=archive"))
			return
		}
		if req.CredentialID != "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "credential_id is only valid for source_type=git_private"))
			return
		}
		if req.UploadID == "" {
			c.JSON(http.StatusBadRequest, errResponse("invalid_body", "upload_id is required for source_type=archive"))
			return
		}
		if _, err := uuid.Parse(req.UploadID); err != nil {
			c.JSON(http.StatusBadRequest, errResponse("invalid_upload_id", "upload_id must be a valid UUID"))
			return
		}
	default:
		c.JSON(http.StatusBadRequest, errResponse("invalid_source_type",
			"source_type must be one of git_public, git_private, archive"))
		return
	}

	// Validate Branch.
	if len(req.Branch) > 100 {
		c.JSON(http.StatusBadRequest, errResponse("invalid_branch", "branch name must be 100 characters or fewer"))
		return
	}

	// Validate BuildArgs.
	if len(req.BuildArgs) > 20 {
		c.JSON(http.StatusBadRequest, errResponse("too_many_build_args", "maximum 20 build args allowed"))
		return
	}
	for key := range req.BuildArgs {
		if !buildArgKeyPattern.MatchString(key) {
			c.JSON(http.StatusBadRequest, errResponse("invalid_build_arg_key",
				"build arg keys must match [A-Z_][A-Z0-9_]{0,63}: "+key))
			return
		}
	}

	// Syntactic check only at API time. Full validation (DNS + IP filter
	// + IP pinning) lives in the worker, immediately before clone — see
	// clone.ValidateRepoURL caller in pipeline/runner.go. TOCTOU window
	// is microseconds, not minutes. Archive sources have no URL to check.
	if req.SourceType != queue.SourceArchive {
		if err := clone.ValidateRepoURLSyntactic(req.RepoURL); err != nil {
			c.JSON(http.StatusBadRequest, errResponse("invalid_repo_url", err.Error()))
			return
		}
	}

	// Check concurrency limit.
	active, err := h.Queue.CountActive(c.Request.Context(), req.UserID)
	if err != nil {
		h.Log.Error("failed to check active builds", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to check build limits"))
		return
	}
	if active >= h.Cfg.MaxConcurrentBuildsPerUser {
		c.JSON(http.StatusTooManyRequests, errResponse("concurrency_limit",
			"maximum concurrent builds reached — wait for a build to complete"))
		return
	}

	// Enqueue the job.
	job := queue.Job{
		DeployID:     req.DeployID,
		UserID:       req.UserID,
		SourceType:   req.SourceType,
		RepoURL:      req.RepoURL,
		Branch:       req.Branch,
		UploadID:     req.UploadID,
		CredentialID: req.CredentialID,
		BuildArgs:    req.BuildArgs,
		QueuedAt:     time.Now().UTC(),
	}

	jobID, err := h.Queue.Enqueue(c.Request.Context(), job)
	if err != nil {
		h.Log.Error("failed to enqueue build job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to enqueue build"))
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":       jobID,
		"deploy_id":    req.DeployID,
		"status":       "queued",
		"logs_channel": "logs:" + req.DeployID,
	})
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "builder-svc",
	})
}

// errResponse builds a standard JSON error body.
func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
