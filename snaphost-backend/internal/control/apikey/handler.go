package apikey

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler serves the public key-management endpoints.
type Handler struct {
	repo *Repository
	log  *zap.Logger
}

// NewHandler builds a Handler.
func NewHandler(repo *Repository, log *zap.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}

// userIDFromHeader reads the identity header written by authentication middleware.
func userIDFromHeader(c *gin.Context) (uuid.UUID, error) {
	return uuid.Parse(c.GetHeader("X-User-ID"))
}

// createKeyRequest is the body of POST /api/v1/keys.
type createKeyRequest struct {
	Name string `json:"name"`
}

// CreateKey handles POST /api/v1/keys — mints a key for the authenticated user
// and returns the plaintext exactly once.
func (h *Handler) CreateKey(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", "X-User-ID header missing or invalid"))
		return
	}
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		// A missing/empty body is fine (name is optional); reject only malformed JSON.
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}
	if len(req.Name) > 100 {
		c.JSON(http.StatusBadRequest, errResponse("invalid_name", "name must be at most 100 characters"))
		return
	}

	gen, err := Generate()
	if err != nil {
		h.log.Error("generate api key failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to generate key"))
		return
	}
	id, createdAt, err := h.repo.Create(c.Request.Context(), userID, gen.Hash, gen.Prefix, req.Name)
	if err != nil {
		h.log.Error("create api key failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to store key"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         id.String(),
		"key":        gen.Plaintext, // shown once; never retrievable again
		"prefix":     gen.Prefix,
		"name":       req.Name,
		"created_at": createdAt,
	})
}

// ListKeys handles GET /api/v1/keys — returns the user's active keys (metadata
// only, never the secret).
func (h *Handler) ListKeys(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", "X-User-ID header missing or invalid"))
		return
	}
	keys, err := h.repo.List(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list api keys failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list keys"))
		return
	}
	if keys == nil {
		keys = []Info{}
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// RevokeKey handles DELETE /api/v1/keys/:id — revokes one of the user's keys.
func (h *Handler) RevokeKey(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", "X-User-ID header missing or invalid"))
		return
	}
	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_id", "key id must be a valid UUID"))
		return
	}
	if err := h.repo.Revoke(c.Request.Context(), userID, keyID); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, errResponse("not_found", "no active key with that id"))
			return
		}
		h.log.Error("revoke api key failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to revoke key"))
		return
	}
	c.Status(http.StatusNoContent)
}
