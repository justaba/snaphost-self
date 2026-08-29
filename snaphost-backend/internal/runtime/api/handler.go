// Package api provides HTTP handlers for the runner-svc internal API.
// All endpoints are service-to-service and require X-Webhook-Secret authentication.
package api

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/internal/runtime/runner"
)

// Handler holds dependencies for all HTTP endpoint handlers.
type Handler struct {
	svc *runner.Service
	log *zap.Logger
}

// NewHandler creates a new API handler.
func NewHandler(svc *runner.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// deployRequest is the JSON body for POST /internal/deploys.
type deployRequest struct {
	DeployID   string            `json:"deploy_id" binding:"required"`
	UserID     string            `json:"user_id" binding:"required"`
	ImageRef   string            `json:"image_ref" binding:"required"`
	Env        map[string]string `json:"env"`
	Port       int               `json:"port" binding:"required"`
	TTLMinutes int               `json:"ttl_minutes"`
}

// Deploy handles POST /internal/deploys — creates and runs a new deployment.
func (h *Handler) Deploy(c *gin.Context) {
	var req deployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	result, err := h.svc.Deploy(c.Request.Context(), runner.DeployRequest{
		DeployID:   req.DeployID,
		UserID:     req.UserID,
		ImageRef:   req.ImageRef,
		Env:        req.Env,
		Port:       req.Port,
		TTLMinutes: req.TTLMinutes,
	})
	if err != nil {
		var verr *runner.ValidationError
		var perr *runner.ProbeError
		switch {
		case errors.As(err, &perr):
			// The image is fine and the container started; it just never
			// served. Retrying it would fail identically, so this is a 4xx:
			// the saga must compensate and refund rather than requeue.
			h.log.Warn("deploy failed liveness probe",
				zap.String("deploy_id", req.DeployID),
				zap.Error(err),
			)
			c.JSON(http.StatusUnprocessableEntity, errResponse("probe_failed", perr.Error()))
			return
		case errors.As(err, &verr):
			h.log.Warn("deploy validation failed",
				zap.String("deploy_id", req.DeployID),
				zap.Error(err),
			)
			c.JSON(http.StatusBadRequest, errResponse("validation_failed", verr.Error()))
			return
		case errors.Is(err, runner.ErrAlreadyRunning):
			c.JSON(http.StatusConflict, errResponse("already_running", err.Error()))
			return
		}
		h.log.Error("deploy failed",
			zap.String("deploy_id", req.DeployID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("deploy_failed", err.Error()))
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"deploy_id":    result.DeployID,
		"endpoint_url": result.EndpointURL,
		"container_id": result.ContainerID,
		"status":       "running",
	})
}

// undeployRequest is the JSON body for DELETE /internal/deploys/:id.
type undeployRequest struct {
	ContainerID string `json:"container_id" binding:"required"`
}

// Undeploy handles DELETE /internal/deploys/:id — stops and removes a deployment.
func (h *Handler) Undeploy(c *gin.Context) {
	deployID := c.Param("id")

	var req undeployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	if err := h.svc.Undeploy(c.Request.Context(), deployID, req.ContainerID); err != nil {
		var verr *runner.ValidationError
		if errors.As(err, &verr) {
			h.log.Warn("undeploy validation failed",
				zap.String("deploy_id", deployID),
				zap.Error(err),
			)
			c.JSON(http.StatusBadRequest, errResponse("validation_failed", verr.Error()))
			return
		}
		h.log.Error("undeploy failed",
			zap.String("deploy_id", deployID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("undeploy_failed", err.Error()))
		return
	}

	c.Status(http.StatusNoContent)
}

// DeployStatus handles GET /internal/deploys/:id/status — returns container health.
func (h *Handler) DeployStatus(c *gin.Context) {
	containerID := c.Query("container_id")
	if containerID == "" {
		c.JSON(http.StatusBadRequest, errResponse("missing_param", "container_id query parameter is required"))
		return
	}

	status, err := h.svc.HealthCheck(c.Request.Context(), containerID)
	if err != nil {
		h.log.Error("health check failed",
			zap.String("container_id", containerID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("health_check_failed", err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"running": status.Running,
		"message": status.Message,
	})
}

// WebhookSecretMiddleware returns a Gin middleware that validates the
// X-Webhook-Secret header using constant-time comparison to prevent timing attacks.
func WebhookSecretMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		provided := c.GetHeader("X-Webhook-Secret")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errResponse("unauthorized", "invalid webhook secret"))
			return
		}
		c.Next()
	}
}

// errResponse builds a standard JSON error body.
func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
