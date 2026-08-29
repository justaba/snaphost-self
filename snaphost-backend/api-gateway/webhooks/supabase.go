package webhooks

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"snaphost/api-gateway/config"
)

// BillingClient interface for interacting with the user-billing service
type BillingClient interface {
	CreateUser(ctx context.Context, req CreateUserRequest) error
}

// CreateUserRequest payload for user creation
type CreateUserRequest struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// WebhookHandler handles incoming webhooks
type WebhookHandler struct {
	cfg     *config.Config
	billing BillingClient
	log     *zap.Logger
}

// NewWebhookHandler creates a new WebhookHandler
func NewWebhookHandler(cfg *config.Config, billing BillingClient, log *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		cfg:     cfg,
		billing: billing,
		log:     log,
	}
}

// supabaseWebhookPayload represents the expected payload from Supabase
type supabaseWebhookPayload struct {
	Type   string `json:"type"`
	Table  string `json:"table"`
	Schema string `json:"schema"`
	Record struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"record"`
}

// SupabaseUserCreated handles the Supabase Database Webhook for user insertion
func (h *WebhookHandler) SupabaseUserCreated(c *gin.Context) {
	secret := c.GetHeader("X-Webhook-Secret")
	if secret != h.cfg.SupabaseWebhookSecret {
		h.log.Warn("Invalid webhook secret attempt")
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload supabaseWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	// Accept either schema/table the trigger chain may target:
	//   - public.profiles (default — INSERT mirrors auth.users via handle_new_user trigger)
	//   - auth.users / public.users (if a webhook is configured directly on the auth table)
	if payload.Type != "INSERT" || (payload.Table != "profiles" && payload.Table != "users") {
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	if payload.Record.ID == "" || payload.Record.Email == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid user record"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	req := CreateUserRequest{
		ID:    payload.Record.ID,
		Email: payload.Record.Email,
	}

	if err := h.billing.CreateUser(ctx, req); err != nil {
		h.log.Error("Failed to create user in billing service", zap.Error(err), zap.String("user_id", req.ID))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.log.Info("Successfully synced new Supabase user", zap.String("user_id", req.ID), zap.String("email", req.Email))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Register maps the webhook routes to the handler
func Register(r *gin.Engine, h *WebhookHandler) {
	r.POST("/internal/webhooks/supabase", h.SupabaseUserCreated)
}
