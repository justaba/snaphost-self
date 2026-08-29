package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RoleHeader carries the caller's role from api-gateway. The gateway's Enrich
// middleware sets it from the verified JWT (or API key) and deletes it when
// the request is unauthenticated, so a client cannot supply its own — the same
// trust model X-User-ID already relies on. user-billing publishes no host port,
// so the gateway is the only reachable caller.
const RoleHeader = "X-User-Role"

// RoleAdmin is the role name the Supabase `snaphost_role` claim must carry.
const RoleAdmin = "admin"

// Pagination bounds. The maximum exists so one request cannot ask for the
// whole deploys table and time out behind the gateway.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Store is the read surface the handler needs. *Repository satisfies it;
// tests substitute fakes.
type Store interface {
	Overview(ctx context.Context) (*Overview, error)
	ListUsers(ctx context.Context, f Filter) (*Page[UserSummary], error)
	GetUser(ctx context.Context, userID uuid.UUID) (*UserDetail, error)
	ListDeploys(ctx context.Context, f Filter) (*Page[DeployRow], error)
	GetDeploy(ctx context.Context, deployID uuid.UUID) (*DeployDetail, error)
	ListTransactions(ctx context.Context, f Filter) (*Page[LedgerRow], error)
	ListDomains(ctx context.Context, f Filter) (*Page[DomainRow], error)
	ListProjects(ctx context.Context, userID uuid.UUID) ([]ProjectRow, error)
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKeyRow, error)
}

// Handler exposes the admin read endpoints.
type Handler struct {
	store Store
	log   *zap.Logger
}

// NewHandler creates an admin HTTP handler.
func NewHandler(store Store, log *zap.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// RequireAdmin refuses any request whose forwarded role is not admin.
//
// api-gateway already enforces this through Casbin on the URL path, so this is
// the second of two checks. It is worth having: the gateway's policy lives in
// a CSV that a new route can be added without, and this service would then
// serve every account's data to any authenticated user. Authorisation for
// operator data should not depend on remembering to edit a separate file.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.EqualFold(strings.TrimSpace(c.GetHeader(RoleHeader)), RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, errResponse("forbidden", "admin role required"))
			return
		}
		c.Next()
	}
}

// Overview handles GET /api/v1/admin/overview.
func (h *Handler) Overview(c *gin.Context) {
	o, err := h.store.Overview(c.Request.Context())
	if err != nil {
		h.fail(c, "failed to load overview", err)
		return
	}
	c.JSON(http.StatusOK, o)
}

// ListUsers handles GET /api/v1/admin/users.
func (h *Handler) ListUsers(c *gin.Context) {
	page, err := h.store.ListUsers(c.Request.Context(), h.filter(c))
	if err != nil {
		h.fail(c, "failed to list users", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// GetUser handles GET /api/v1/admin/users/:id.
func (h *Handler) GetUser(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	detail, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, errResponse("user_not_found", "no such user"))
			return
		}
		h.fail(c, "failed to get user", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// UserDeploys handles GET /api/v1/admin/users/:id/deploys.
func (h *Handler) UserDeploys(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	f := h.filter(c)
	f.UserID = &userID
	page, err := h.store.ListDeploys(c.Request.Context(), f)
	if err != nil {
		h.fail(c, "failed to list user deploys", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// UserTransactions handles GET /api/v1/admin/users/:id/transactions.
func (h *Handler) UserTransactions(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	f := h.filter(c)
	f.UserID = &userID
	page, err := h.store.ListTransactions(c.Request.Context(), f)
	if err != nil {
		h.fail(c, "failed to list user transactions", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// UserDomains handles GET /api/v1/admin/users/:id/domains.
func (h *Handler) UserDomains(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	f := h.filter(c)
	f.UserID = &userID
	page, err := h.store.ListDomains(c.Request.Context(), f)
	if err != nil {
		h.fail(c, "failed to list user domains", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// UserKeys handles GET /api/v1/admin/users/:id/keys.
func (h *Handler) UserKeys(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	keys, err := h.store.ListAPIKeys(c.Request.Context(), userID)
	if err != nil {
		h.fail(c, "failed to list user api keys", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": keys})
}

// UserProjects handles GET /api/v1/admin/users/:id/projects.
func (h *Handler) UserProjects(c *gin.Context) {
	userID, ok := pathUUID(c, "id", "invalid_user_id")
	if !ok {
		return
	}

	projects, err := h.store.ListProjects(c.Request.Context(), userID)
	if err != nil {
		h.fail(c, "failed to list user projects", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": projects})
}

// ListDeploys handles GET /api/v1/admin/deploys.
func (h *Handler) ListDeploys(c *gin.Context) {
	page, err := h.store.ListDeploys(c.Request.Context(), h.filter(c))
	if err != nil {
		h.fail(c, "failed to list deploys", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// GetDeploy handles GET /api/v1/admin/deploys/:id.
func (h *Handler) GetDeploy(c *gin.Context) {
	deployID, ok := pathUUID(c, "id", "invalid_deploy_id")
	if !ok {
		return
	}

	detail, err := h.store.GetDeploy(c.Request.Context(), deployID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, errResponse("deploy_not_found", "no such deploy"))
			return
		}
		h.fail(c, "failed to get deploy", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// ListTransactions handles GET /api/v1/admin/transactions.
func (h *Handler) ListTransactions(c *gin.Context) {
	page, err := h.store.ListTransactions(c.Request.Context(), h.filter(c))
	if err != nil {
		h.fail(c, "failed to list transactions", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// ListDomains handles GET /api/v1/admin/domains.
func (h *Handler) ListDomains(c *gin.Context) {
	page, err := h.store.ListDomains(c.Request.Context(), h.filter(c))
	if err != nil {
		h.fail(c, "failed to list domains", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// filter reads the shared query parameters. An unparseable user_id is treated
// as absent rather than as an error: these are list screens, and a malformed
// filter should not turn into a 400 the operator has to decode.
func (h *Handler) filter(c *gin.Context) Filter {
	f := Filter{
		Query:  strings.TrimSpace(c.Query("q")),
		Status: strings.TrimSpace(c.Query("status")),
		Type:   strings.TrimSpace(c.Query("type")),
		Limit:  clampLimit(c.Query("limit")),
		Offset: parseOffset(c.Query("offset")),
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.UserID = &id
		}
	}
	return f
}

func clampLimit(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return DefaultLimit
	}
	if n > MaxLimit {
		return MaxLimit
	}
	return n
}

func parseOffset(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func pathUUID(c *gin.Context, param, errCode string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse(errCode, param+" must be a valid UUID"))
		return uuid.Nil, false
	}
	return id, true
}

// fail logs the cause and returns a generic message. Database errors can carry
// query text and column names; the operator gets those from the service logs,
// not from an HTTP body.
func (h *Handler) fail(c *gin.Context, msg string, err error) {
	h.log.Error(msg, zap.Error(err), zap.String("path", c.Request.URL.Path))
	c.JSON(http.StatusInternalServerError, errResponse("internal_error", msg))
}

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
