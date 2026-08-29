// Package account records who a user_id belongs to.
//
// It is what survived the removal of billing. The wallet package used to own
// both the money and the identity behind it, seeding them from the same
// webhook; the money is gone and the identity is not, because an operator
// still has to be able to name the account behind a deploy without opening a
// second database.
package account

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Repository persists accounts.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates an account repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Upsert records the account behind a user_id. It is idempotent because the
// webhook that calls it can be redelivered. A later call with a non-empty
// email fills one in for a row backfilled without one; an empty email never
// overwrites a stored value, since losing an address to a retry carrying less
// information would be silent.
func (r *Repository) Upsert(ctx context.Context, userID uuid.UUID, email string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users (id, email) VALUES ($1, nullif($2, ''))
		 ON CONFLICT (id) DO UPDATE
		 SET email = coalesce(nullif($2, ''), users.email)`,
		userID, email,
	)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	return nil
}

// Handler serves the internal account endpoints.
type Handler struct {
	repo *Repository
	log  *zap.Logger
}

// NewHandler creates an account handler.
func NewHandler(repo *Repository, log *zap.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

type createUserRequest struct {
	ID    string `json:"id" binding:"required"`
	Email string `json:"email"`
}

// Create records a new account. It is called by the identity provider's
// signup webhook and is the only path by which an email reaches this
// database — the address arrives here once and cannot be reconstructed later.
func (h *Handler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	userID, err := uuid.Parse(req.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_user_id", "id must be a valid UUID"))
		return
	}

	if err := h.repo.Upsert(c.Request.Context(), userID, req.Email); err != nil {
		h.log.Error("failed to record account", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to record account"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{"user_id": userID.String()})
}

// WebhookSecretMiddleware validates the X-Webhook-Secret header with a
// constant-time comparison, so a wrong secret cannot be found by timing.
//
// Duplicated from snaphost/internal/shared rather than imported: user-billing is its
// own module and does not depend on shared today. The duplicate disappears
// when the modules merge.
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

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
