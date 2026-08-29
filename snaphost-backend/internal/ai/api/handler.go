// Package api provides Gin HTTP handlers for the ai-orchestrator's internal API.
package api

import (
	"errors"
	"net/http"

	"snaphost/internal/ai/llm"
	"snaphost/internal/ai/service"

	"github.com/gin-gonic/gin"
)

// Handler holds the injected service for use in HTTP handlers.
type Handler struct {
	svc *service.Service
}

// NewHandler constructs a Handler with its service dependency.
func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register attaches all handler routes to the provided router group.
func (h *Handler) Register(r *gin.RouterGroup) {
	r.POST("/generate-dockerfile", h.GenerateDockerfile)
}

// GenerateDockerfile handles POST /internal/ai/generate-dockerfile.
// It binds the request, delegates to the service, and maps typed errors to
// appropriate HTTP status codes.
func (h *Handler) GenerateDockerfile(c *gin.Context) {
	var req llm.GenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	if req.DeployID == "" || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"details": "deploy_id and user_id are required",
		})
		return
	}

	resp, err := h.svc.GenerateDockerfile(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, llm.ErrRateLimited), errors.Is(err, llm.ErrCircuitOpen):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate_limited"})
		case errors.Is(err, llm.ErrInvalidOutput):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "generation_failed"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "details": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, resp)
}
