package project

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Headers written by the gateway's Enrich middleware, which deletes any
// incoming copy first. That deletion is the anti-spoofing measure.
const (
	userIDHeader = "X-User-ID"
)

// Store is the repository surface the handler needs.
type Store interface {
	ListSummaries(ctx context.Context, userID uuid.UUID) ([]Summary, error)
	PrepareDeletion(ctx context.Context, projectID uuid.UUID) (*Deletion, error)
	Delete(ctx context.Context, projectID, actorUserID uuid.UUID, plannedDeploys []uuid.UUID) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)
}

// RuntimeCleaner removes the Docker state before project rows are deleted.
// Both operations are idempotent, which is what makes a failed request safe to
// repeat.
//
// StopStrict rather than Stop: the ordinary Stop reports success for a deploy
// the runtime refuses to recognise, which is what makes saga compensation safe
// to repeat and is exactly wrong here — the rows naming that container are
// about to be hard-deleted, so a refusal has to reach this handler.
type RuntimeCleaner interface {
	StopStrict(ctx context.Context, deployID, containerID string) error
	RemoveImage(ctx context.Context, deployID, imageRef string) error
}

// Handler serves the project screen: the permanent publish targets, and the
// one destructive action the panel has.
type Handler struct {
	store   Store
	runtime RuntimeCleaner
	log     *zap.Logger
}

// NewHandler builds the project HTTP handler.
func NewHandler(store Store, runtime RuntimeCleaner, log *zap.Logger) *Handler {
	return &Handler{store: store, runtime: runtime, log: log}
}

// List handles GET /api/v1/projects.
func (h *Handler) List(c *gin.Context) {
	userID, ok := h.actor(c)
	if !ok {
		return
	}
	items, err := h.store.ListSummaries(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to list projects", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list projects"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// Delete handles DELETE /api/v1/projects/:id.
//
// The order is the whole design. External state goes before the rows that name
// it, because after the commit there is nothing left to find a stranded
// container or image with. A cleanup failure therefore aborts with 502 having
// deleted nothing, and the operator can fix the daemon and repeat.
func (h *Handler) Delete(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_project_id", "id must be a valid UUID"))
		return
	}

	plan, err := h.store.PrepareDeletion(c.Request.Context(), projectID)
	if err != nil {
		h.refusal(c, err, "failed to prepare project deletion")
		return
	}
	if plan.Project.UserID != actor {
		c.JSON(http.StatusForbidden, errResponse("forbidden", "project belongs to a different user"))
		return
	}

	// The condition has to match the loop below exactly, or a project the loop
	// would touch reaches a nil runtime and panics.
	cleanupNeeded := false
	for _, d := range plan.Deploys {
		if d.ContainerID != "" || d.ImageRef != "" {
			cleanupNeeded = true
			break
		}
	}
	if cleanupNeeded && h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("runtime_unavailable", "runtime cleanup is not configured"))
		return
	}

	// Once destructive cleanup starts it must not inherit a browser disconnect.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The exact set the cleanup touched. The commit refuses if the project has
	// gained anything outside it, because that is a deploy nothing stopped.
	planned := make([]uuid.UUID, 0, len(plan.Deploys))
	for _, d := range plan.Deploys {
		planned = append(planned, d.DeployID)
	}

	for _, d := range plan.Deploys {
		deployID := d.DeployID.String()

		// Stop and RemoveImage are both attempted, never one or the other.
		// Stopping no longer releases the image at all — a stopped deploy has
		// to stay startable — and even the paths that do release it report
		// success for a deploy the runtime refuses to recognise, which is what
		// makes saga compensation safe to repeat. This is the one caller that
		// cannot tolerate either: the row naming the image is about to be
		// deleted. RemoveImage is idempotent.
		if d.ContainerID != "" {
			if err := h.runtime.StopStrict(cleanupCtx, deployID, d.ContainerID); err != nil {
				h.failCleanup(c, projectID, deployID, "stop container", err)
				return
			}
		}
		if d.ImageRef != "" {
			if err := h.runtime.RemoveImage(cleanupCtx, deployID, d.ImageRef); err != nil {
				h.failCleanup(c, projectID, deployID, "remove image", err)
				return
			}
		}
	}

	if err := h.store.Delete(cleanupCtx, projectID, actor, planned); err != nil {
		h.refusal(c, err, "failed to delete project")
		return
	}

	h.log.Info("project deleted",
		zap.String("project_id", projectID.String()),
		zap.String("actor_user_id", actor.String()),
		zap.Int("deploys", len(plan.Deploys)),
	)
	c.Status(http.StatusNoContent)
}

// Audit handles GET /api/v1/audit — the record of destructive actions.
// Without it admin_audit_log is written and never read, which is most of the
// way to not having it.
func (h *Handler) Audit(c *gin.Context) {
	if _, ok := h.actor(c); !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	entries, err := h.store.ListAudit(c.Request.Context(), limit)
	if err != nil {
		h.log.Error("failed to list audit log", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to read the audit log"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": entries, "total": len(entries)})
}

// actor reads the enriched identity. A destructive action that cannot name an
// operator is refused rather than attributed to nobody.
func (h *Handler) actor(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(c.GetHeader(userIDHeader)))
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", "user identity is missing"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) refusal(c *gin.Context, err error, logMsg string) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, errResponse("project_not_found", "no such project"))
	case errors.Is(err, ErrBusy):
		c.JSON(http.StatusConflict, errResponse("project_busy",
			"project has a build or deployment in progress"))
	default:
		h.log.Error(logMsg, zap.Error(err), zap.String("path", c.Request.URL.Path))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", logMsg))
	}
}

// failCleanup refuses the deletion because host state is still there. Nothing
// has been removed from the database at this point.
func (h *Handler) failCleanup(c *gin.Context, projectID uuid.UUID, deployID, step string, err error) {
	h.log.Error("project runtime cleanup failed",
		zap.String("project_id", projectID.String()),
		zap.String("deploy_id", deployID),
		zap.String("step", step),
		zap.Error(err),
	)
	c.JSON(http.StatusBadGateway, errResponse("cleanup_failed",
		"failed to remove project runtime resources; nothing was deleted"))
}

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
