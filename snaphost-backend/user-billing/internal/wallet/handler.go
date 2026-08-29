package wallet

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler exposes HTTP endpoints for wallet operations.
type Handler struct {
	svc           *Service
	log           *zap.Logger
	webhookSecret string
	initialBal    int64
}

// NewHandler creates a new wallet HTTP handler.
func NewHandler(svc *Service, log *zap.Logger, webhookSecret string, initialBalance int64) *Handler {
	return &Handler{
		svc:           svc,
		log:           log,
		webhookSecret: webhookSecret,
		initialBal:    initialBalance,
	}
}

// GetBilling returns the authenticated user's wallet balance and reserved funds.
func (h *Handler) GetBilling(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_user_id", "X-User-ID header is missing or invalid"))
		return
	}

	w, err := h.svc.Get(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrWalletNotFound) {
			c.JSON(http.StatusNotFound, errResponse("wallet_not_found", "no wallet found for this user"))
			return
		}
		h.log.Error("failed to get wallet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to retrieve billing information"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":  w.UserID.String(),
		"balance":  w.Balance,
		"reserved": w.Reserved,
	})
}

// topupRequest is the JSON body for the internal topup endpoint. The user is
// named explicitly rather than taken from a forwarded header, because the
// caller is another service crediting somebody else's wallet, not that user's
// own browser.
type topupRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	Amount         int64  `json:"amount" binding:"required,gt=0"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

// Topup credits vibecoins to a wallet. It is an internal endpoint behind
// X-Webhook-Secret, never reachable by a user.
//
// It was public until 2026-08-07, taking the amount straight from the request
// body of whoever was logged in: any account, or any `sk_` key, could grant
// itself an unlimited balance and therefore unlimited build and runtime
// resources. Nothing verified that money had changed hands, because nothing
// collects money yet.
//
// When payments are integrated, the provider's webhook handler is what calls
// this — after verifying the callback signature — passing the provider's own
// payment id as the idempotency key so a redelivered callback credits once.
func (h *Handler) Topup(c *gin.Context) {
	var req topupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_user_id", "user_id must be a valid UUID"))
		return
	}

	txID, err := h.svc.Topup(c.Request.Context(), userID, req.Amount, req.IdempotencyKey)
	if err != nil {
		h.log.Error("failed to topup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to process topup"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"tx_id": txID})
}

// createUserRequest is the JSON body for the internal user creation webhook.
type createUserRequest struct {
	ID    string `json:"id" binding:"required"`
	Email string `json:"email" binding:"required"`
}

// CreateUser handles the internal webhook for new user registration.
// It creates a wallet with the configured initial vibecoin balance.
func (h *Handler) CreateUser(c *gin.Context) {
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

	if err := h.svc.Create(c.Request.Context(), userID, req.Email, h.initialBal); err != nil {
		h.log.Error("failed to create wallet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to create user wallet"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{"user_id": userID.String(), "balance": h.initialBal})
}

// reserveRequest is the JSON body for the internal reserve endpoint.
type reserveRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	DeployID       string `json:"deploy_id" binding:"required"`
	Amount         int64  `json:"amount" binding:"required,gt=0"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

// Reserve handles the internal endpoint for reserving vibecoins for a deploy.
func (h *Handler) Reserve(c *gin.Context) {
	var req reserveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_user_id", "user_id must be a valid UUID"))
		return
	}

	deployID, err := uuid.Parse(req.DeployID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "deploy_id must be a valid UUID"))
		return
	}

	txID, err := h.svc.Reserve(c.Request.Context(), ReserveRequest{
		UserID:         userID,
		DeployID:       deployID,
		Amount:         req.Amount,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			c.JSON(http.StatusPaymentRequired, errResponse("insufficient_balance", "not enough vibecoins"))
			return
		}
		if errors.Is(err, ErrWalletNotFound) {
			c.JSON(http.StatusNotFound, errResponse("wallet_not_found", "no wallet found for this user"))
			return
		}
		h.log.Error("failed to reserve", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to reserve vibecoins"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"tx_id": txID})
}

// commitRequest is the JSON body for the internal commit endpoint.
type commitRequest struct {
	TxID string `json:"tx_id" binding:"required"`
}

// Commit handles the internal endpoint for committing a reservation.
func (h *Handler) Commit(c *gin.Context) {
	var req commitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	txID, err := uuid.Parse(req.TxID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_tx_id", "tx_id must be a valid UUID"))
		return
	}

	if err := h.svc.Commit(c.Request.Context(), txID); err != nil {
		if errors.Is(err, ErrTransactionNotFound) {
			c.JSON(http.StatusNotFound, errResponse("transaction_not_found", "no pending reservation found"))
			return
		}
		h.log.Error("failed to commit", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to commit reservation"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "committed"})
}

// refundRequest is the JSON body for the internal refund endpoint.
type refundRequest struct {
	TxID string `json:"tx_id" binding:"required"`
}

// Refund handles the internal endpoint for refunding a reservation.
func (h *Handler) Refund(c *gin.Context) {
	var req refundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	txID, err := uuid.Parse(req.TxID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_tx_id", "tx_id must be a valid UUID"))
		return
	}

	if err := h.svc.Refund(c.Request.Context(), txID); err != nil {
		if errors.Is(err, ErrTransactionNotFound) {
			c.JSON(http.StatusNotFound, errResponse("transaction_not_found", "no pending reservation found"))
			return
		}
		h.log.Error("failed to refund", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to refund reservation"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "refunded"})
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

// userIDFromHeader extracts and parses the X-User-ID header set by api-gateway.
func userIDFromHeader(c *gin.Context) (uuid.UUID, error) {
	return uuid.Parse(c.GetHeader("X-User-ID"))
}

// errResponse builds a standard JSON error body.
func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
