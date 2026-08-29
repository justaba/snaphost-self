// Package api provides HTTP handlers for the builder-svc API process.
package api

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/internal/builder/config"
	"snaphost/internal/builder/queue"
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

// Build handles POST /api/v1/build. It is a shell around Enqueuer.Enqueue:
// authenticate, decode, delegate, and turn a *RequestError back into the
// status it already carries.
func (h *Handler) Build(c *gin.Context) {
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

	enq := &Enqueuer{Queue: h.Queue, Cfg: h.Cfg, Log: h.Log}
	jobID, err := enq.Enqueue(c.Request.Context(), req)
	if err != nil {
		var reqErr *RequestError
		if errors.As(err, &reqErr) {
			c.JSON(reqErr.Status, errResponse(reqErr.Code, reqErr.Message))
			return
		}
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
