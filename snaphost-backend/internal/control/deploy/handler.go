// Package deploy provides HTTP handlers for deploy-related endpoints.
// Internal endpoints are called by runner-svc to update deploy lifecycle
// state. Public endpoints are exposed through api-gateway for the
// frontend to create, inspect, and list deploys; CreateDeploy enqueues
// a saga and returns 202 immediately.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"snaphost/internal/control/logs"
	"snaphost/internal/control/project"
	"snaphost/internal/control/saga"
	"snaphost/internal/gitcreds"
	"snaphost/internal/uploads"
)

// Repo is the data access surface required by Handler. *Repository
// satisfies this interface; tests substitute fakes.
type Repo interface {
	Create(ctx context.Context, d Deploy) error
	Get(ctx context.Context, deployID uuid.UUID) (*Deploy, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Deploy, error)
	UpdateStatus(ctx context.Context, deployID uuid.UUID, status string, failureReason *string) error
	SetRunning(ctx context.Context, deployID uuid.UUID, imageRef, endpointURL, subdomain, containerID string, ttlExpiresAt time.Time) error
	MarkDeleted(ctx context.Context, deployID uuid.UUID) error
	MarkImageDeleted(ctx context.Context, deployID uuid.UUID) error
	BeginRestart(ctx context.Context, deployID uuid.UUID, defaultPort int) (*RestartTarget, error)
	AbandonRestart(ctx context.Context, deployID uuid.UUID, reason string) error
	GetSaga(ctx context.Context, deployID uuid.UUID) (*SagaView, error)
	FindExpiredWithDetails(ctx context.Context, limit int) ([]ExpiredDeploy, error)
	FindImagesPendingCleanup(ctx context.Context, limit int) ([]ImageCleanup, error)
	FindRouteByHost(ctx context.Context, host string) (*RouteInfo, error)
}

// Projects resolves the permanent publish target a new deploy belongs to
// (Task 16a). *project.Repository satisfies it; nil disables the linkage and
// deploys are created without a project, as they were before Task 16.
type Projects interface {
	Ensure(ctx context.Context, userID uuid.UUID, sourceKey string) (*project.Project, error)
}

// RuntimeCleaner is the saga runtime surface plus explicit artifact cleanup
// for terminal deploys that never acquired a container.
type RuntimeCleaner interface {
	saga.RunnerClient
	RemoveImage(ctx context.Context, deployID, imageRef string) error
}

// Handler exposes HTTP endpoints for deploy operations.
type Handler struct {
	repo           Repo
	projects       Projects
	log            *zap.Logger
	sagaQueue      *saga.Queue
	runner         RuntimeCleaner
	logReader      *logs.Reader
	uploads        *uploads.Store
	maxUploadBytes int64
	uploadTTL      time.Duration
	creds          *gitcreds.Store
	credTTL        time.Duration
	restartPort    int
	restartTTLMin  int
}

// HandlerOption configures optional handler behaviour. It is variadic so the
// already long constructor does not grow another two positional ints that
// every call site and test would have to carry.
type HandlerOption func(*Handler)

// WithRestartDefaults supplies the port and TTL a restarted deploy gets when
// the saga recorded none. They are the same values the orchestrator uses, and
// they come from one place in main so a restart and a first deploy cannot
// disagree about where the application listens.
func WithRestartDefaults(port, ttlMinutes int) HandlerOption {
	return func(h *Handler) {
		if port > 0 {
			h.restartPort = port
		}
		if ttlMinutes > 0 {
			h.restartTTLMin = ttlMinutes
		}
	}
}

// NewHandler creates a new deploy HTTP handler. sagaQueue, logReader, and
// uploads may be zero values when the corresponding subsystem is disabled
// (CreateDeploy returns 503 if the saga is off; GetLogs returns 503 if log
// history is off; UploadArchive returns 503 if the upload store is off).
// projects may be nil, in which case deploys are created without a project.
func NewHandler(repo Repo, projects Projects, log *zap.Logger, sagaQueue *saga.Queue, runner RuntimeCleaner, logReader *logs.Reader, uploads *uploads.Store, maxUploadBytes int64, uploadTTL time.Duration, creds *gitcreds.Store, credTTL time.Duration, opts ...HandlerOption) *Handler {
	h := &Handler{
		repo:           repo,
		projects:       projects,
		log:            log,
		sagaQueue:      sagaQueue,
		runner:         runner,
		logReader:      logReader,
		uploads:        uploads,
		maxUploadBytes: maxUploadBytes,
		uploadTTL:      uploadTTL,
		creds:          creds,
		credTTL:        credTTL,
		restartPort:    defaultRestartPort,
		restartTTLMin:  defaultRestartTTLMinutes,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Fallbacks for a deploy whose saga recorded no port, and for a TTL nobody
// configured. They mirror the orchestrator's own defaults.
const (
	defaultRestartPort       = 3000
	defaultRestartTTLMinutes = 30
)

// updateStatusRequest is the JSON body for POST /internal/deploys/:id/status.
type updateStatusRequest struct {
	Status        string  `json:"status" binding:"required"`
	FailureReason *string `json:"failure_reason"`
}

// UpdateStatus handles POST /internal/deploys/:id/status — updates the deploy's
// status and optionally sets a failure reason.
func (h *Handler) UpdateStatus(c *gin.Context) {
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	var req updateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	if err := h.repo.UpdateStatus(c.Request.Context(), deployID, req.Status, req.FailureReason); err != nil {
		h.log.Error("failed to update deploy status",
			zap.String("deploy_id", deployID.String()),
			zap.String("status", req.Status),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to update deploy status"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// setRunningRequest is the JSON body for POST /internal/deploys/:id/running.
type setRunningRequest struct {
	ImageRef     string    `json:"image_ref" binding:"required"`
	EndpointURL  string    `json:"endpoint_url" binding:"required"`
	Subdomain    string    `json:"subdomain" binding:"required"`
	ContainerID  string    `json:"container_id" binding:"required"`
	TTLExpiresAt time.Time `json:"ttl_expires_at" binding:"required"`
}

// SetRunning handles POST /internal/deploys/:id/running — transitions the deploy
// to running status with all runtime details populated.
func (h *Handler) SetRunning(c *gin.Context) {
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	var req setRunningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	if err := h.repo.SetRunning(c.Request.Context(), deployID, req.ImageRef, req.EndpointURL, req.Subdomain, req.ContainerID, req.TTLExpiresAt); err != nil {
		h.log.Error("failed to set deploy running",
			zap.String("deploy_id", deployID.String()),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to set deploy running"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "running"})
}

// MarkImageDeleted records successful local artifact cleanup. The runtime only
// calls this after Docker confirms the image is absent.
func (h *Handler) MarkImageDeleted(c *gin.Context) {
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}
	if err := h.repo.MarkImageDeleted(c.Request.Context(), deployID); err != nil {
		h.log.Error("failed to mark deploy image deleted", zap.String("deploy_id", deployID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to record image cleanup"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ListExpired handles GET /internal/deploys/expired?limit=N — returns deploys
// with status='running' whose TTL has expired, for the watchdog to stop.
func (h *Handler) ListExpired(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	deploys, err := h.repo.FindExpiredWithDetails(c.Request.Context(), limit)
	if err != nil {
		h.log.Error("failed to list expired deploys", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list expired deploys"))
		return
	}

	if deploys == nil {
		deploys = []ExpiredDeploy{}
	}

	c.JSON(http.StatusOK, deploys)
}

// ListImagesPendingCleanup returns terminal deploy artifacts the watchdog must
// remove. Successful removals disappear after MarkImageDeleted.
func (h *Handler) ListImagesPendingCleanup(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	images, err := h.repo.FindImagesPendingCleanup(c.Request.Context(), limit)
	if err != nil {
		h.log.Error("failed to list deploy images pending cleanup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list image cleanup work"))
		return
	}
	if images == nil {
		images = []ImageCleanup{}
	}
	c.JSON(http.StatusOK, images)
}

// LookupRouteByHost handles GET /internal/routes?host=<host>. It returns the
// minimal runtime mapping needed by the central router.
func (h *Handler) LookupRouteByHost(c *gin.Context) {
	host := c.Query("host")
	if host == "" {
		c.JSON(http.StatusBadRequest, errResponse("missing_host", "host query parameter is required"))
		return
	}

	route, err := h.repo.FindRouteByHost(c.Request.Context(), host)
	if err != nil {
		if errors.Is(err, ErrRouteNotFound) {
			c.JSON(http.StatusNotFound, errResponse("route_not_found", "no running deploy for host"))
			return
		}
		h.log.Error("failed to lookup route by host", zap.String("host", host), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to lookup route"))
		return
	}

	c.JSON(http.StatusOK, route)
}

// CreateDeployRequest is the body of POST /api/v1/deploys. SourceType
// defaults to git_public, which keeps the pre-14b request shape
// ({repo_url, branch}) working unchanged. The field matrix is enforced
// by validate(): git_* requires repo_url+branch and no upload_id;
// archive requires upload_id and no repo_url/branch.
type CreateDeployRequest struct {
	SourceType string `json:"source_type"`
	RepoURL    string `json:"repo_url"`
	Branch     string `json:"branch"`
	UploadID   string `json:"upload_id"`
	// GitToken is the short-lived credential for source_type=git_private
	// (a PAT or OAuth token). It is held in memory with a TTL and never
	// persisted or logged; only its opaque id travels further.
	GitToken string `json:"git_token"`
	// GitUsername optionally overrides the basic-auth username sent with
	// GitToken (defaults to "git", which GitHub/GitLab accept for PATs).
	GitUsername string `json:"git_username"`
	// ProjectKey is a client-supplied stable identity for the publish target.
	//
	// It exists for the archive path. A git deploy derives its project from the
	// repo URL and branch, so successive builds converge and a custom domain
	// can be moved to the newest one. An uploaded tarball has no such identity:
	// without this, every upload becomes its own project, and a domain attached
	// to one can never follow later builds — publishing a new version would
	// mean detaching the domain and asking the customer to edit DNS again.
	//
	// A client that can persist one value between deploys (the MCP server
	// writing it beside the project, an editor extension in its workspace
	// state) should generate one and send it every time.
	ProjectKey string `json:"project_key"`
}

// validate normalizes SourceType (empty → git_public) and enforces the
// per-source field matrix. Returns a user-facing error code and message.
func (r *CreateDeployRequest) validate() (code, message string) {
	if r.SourceType == "" {
		r.SourceType = SourceGitPublic
	}
	if !ValidSourceType(r.SourceType) {
		return "invalid_source_type", "source_type must be one of git_public, git_private, archive"
	}

	if r.ProjectKey != "" {
		switch project.ValidateClientProjectKey(r.ProjectKey) {
		case "project_key_too_long":
			return "invalid_project_key", "project_key must be at most 128 characters"
		case "invalid_project_key":
			return "invalid_project_key",
				"project_key may contain only letters, digits, and - _ . : /"
		}
	}

	switch r.SourceType {
	case SourceGitPublic, SourceGitPrivate:
		if r.UploadID != "" {
			return "invalid_body", "upload_id is only valid for source_type=archive"
		}
		if r.RepoURL == "" {
			return "invalid_body", "repo_url is required for git sources"
		}
		u, err := url.Parse(r.RepoURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "invalid_repo_url", "repo_url must be a valid http(s) URL"
		}
		// Credentials embedded in the URL would end up in the deploy row
		// and logs — the only accepted channel is the git_token field.
		if u.User != nil {
			return "invalid_repo_url", "repo_url must not embed credentials; use git_token"
		}
		if r.Branch == "" {
			return "invalid_body", "branch is required for git sources"
		}
		if r.SourceType == SourceGitPrivate {
			if r.GitToken == "" {
				return "invalid_body", "git_token is required for source_type=git_private"
			}
			if len(r.GitToken) > 512 || len(r.GitUsername) > 128 {
				return "invalid_body", "git_token or git_username is too long"
			}
		} else if r.GitToken != "" || r.GitUsername != "" {
			return "invalid_body", "git_token is only valid for source_type=git_private"
		}
	case SourceArchive:
		if r.RepoURL != "" || r.Branch != "" {
			return "invalid_body", "repo_url and branch are not valid for source_type=archive"
		}
		if r.GitToken != "" || r.GitUsername != "" {
			return "invalid_body", "git_token is only valid for source_type=git_private"
		}
		if r.UploadID == "" {
			return "invalid_body", "upload_id is required for source_type=archive"
		}
		if _, err := uuid.Parse(r.UploadID); err != nil {
			return "invalid_upload_id", "upload_id must be a valid UUID"
		}
	}
	return "", ""
}

// CreateDeploy handles POST /api/v1/deploys — validates the request,
// creates the deploys + deploy_sagas rows, enqueues the saga job, and
// returns 202 immediately. The saga worker drives the rest of the work
// asynchronously.
func (h *Handler) CreateDeploy(c *gin.Context) {
	if h.sagaQueue == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("saga_disabled", "saga worker is not running"))
		return
	}
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}

	var req CreateDeployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}
	if code, message := req.validate(); code != "" {
		c.JSON(http.StatusBadRequest, errResponse(code, message))
		return
	}
	if req.SourceType == SourceGitPrivate && h.creds == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("credentials_disabled",
			"git_private deploys are not available"))
		return
	}
	// Archive deploys must reference a live upload owned by the caller.
	// The blob may expire between this check and the build (short TTL);
	// the builder then fails the deploy and the saga refunds — this check
	// only keeps the obvious mistakes cheap.
	if req.SourceType == SourceArchive {
		if h.uploads == nil {
			c.JSON(http.StatusServiceUnavailable, errResponse("uploads_disabled", "archive uploads are not available"))
			return
		}
		owner, err := h.uploads.Owner(c.Request.Context(), req.UploadID)
		if err != nil {
			if errors.Is(err, uploads.ErrNotFound) {
				c.JSON(http.StatusNotFound, errResponse("upload_not_found", "upload_id does not exist or has expired"))
				return
			}
			h.log.Error("upload owner lookup failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to verify upload"))
			return
		}
		if owner != userID.String() {
			c.JSON(http.StatusForbidden, errResponse("forbidden", "upload belongs to a different user"))
			return
		}
	}

	deployID := uuid.New()

	var uploadID *string
	if req.UploadID != "" {
		uploadID = &req.UploadID
	}

	// git_private: park the credential in memory under a fresh id. Only
	// the id goes into the saga job / deploy_sagas; the builder deletes
	// the secret right after the clone, the TTL is the backstop.
	var credentialID string
	if req.SourceType == SourceGitPrivate {
		id, err := h.creds.Put(c.Request.Context(), userID.String(), req.GitUsername, req.GitToken, h.credTTL)
		if err != nil {
			h.log.Error("store git credential failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to store git credential"))
			return
		}
		credentialID = id
	}

	// Resolve the permanent publish target this build belongs to. A repeat
	// deploy of the same source joins the project it created the first time,
	// which is what lets a domain attached to that project outlive any one
	// deploy. A failure here is not worth refusing the deploy over: the row
	// is simply created without a project.
	var projectID *uuid.UUID
	if h.projects != nil {
		sourceKey := project.ArchiveSourceKey(deployID.String())
		switch {
		case req.ProjectKey != "":
			// An explicit key wins over anything derived. It is the only way an
			// archive deploy can rejoin the project its predecessor created,
			// and therefore the only way a custom domain on that project can
			// follow a new upload.
			sourceKey = project.ClientSourceKey(req.ProjectKey)
		case req.SourceType != SourceArchive:
			sourceKey = project.GitSourceKey(req.RepoURL, req.Branch)
		}
		p, err := h.projects.Ensure(c.Request.Context(), userID, sourceKey)
		if err != nil {
			h.log.Error("resolve project failed", zap.String("deploy_id", deployID.String()), zap.Error(err))
		} else {
			projectID = &p.ID
		}
	}

	if err := h.repo.Create(c.Request.Context(), Deploy{
		ID:         deployID,
		UserID:     userID,
		ProjectID:  projectID,
		SourceType: req.SourceType,
		RepoURL:    req.RepoURL,
		Branch:     req.Branch,
		UploadID:   uploadID,
		Status:     "pending",
	}); err != nil {
		h.log.Error("create deploy row failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to create deploy"))
		return
	}

	if err := h.sagaQueue.Enqueue(c.Request.Context(), saga.SagaJob{
		DeployID:     deployID.String(),
		UserID:       userID.String(),
		SourceType:   req.SourceType,
		RepoURL:      req.RepoURL,
		Branch:       req.Branch,
		UploadID:     req.UploadID,
		CredentialID: credentialID,
		EnqueuedAt:   time.Now().UTC(),
	}); err != nil {
		h.log.Error("enqueue saga failed", zap.String("deploy_id", deployID.String()), zap.Error(err))
		// The deploy row exists but the saga is not running. The resume
		// sweeper will pick it up after the 5-minute threshold; meanwhile
		// surface the error to the caller so they can retry.
		c.JSON(http.StatusInternalServerError, errResponse("enqueue_failed", "failed to enqueue saga"))
		return
	}

	// logs_channel and events_channel used to be here, naming the Redis
	// channels a client was invited to subscribe to. There is no broker to
	// subscribe to any more, and there never was one a client could reach —
	// Redis was on an internal network. What a caller actually uses is below.
	c.JSON(http.StatusAccepted, gin.H{
		"deploy_id":  deployID.String(),
		"status":     "pending",
		"poll_url":   "/api/v1/deploys/" + deployID.String(),
		"logs_url":   "/api/v1/deploys/" + deployID.String() + "/logs",
		"stream_url": "/ws/logs/" + deployID.String(),
	})
}

// UploadArchive handles POST /api/v1/deploys/upload — accepts a raw
// tar.gz body (bounded), streams it to a file with a short TTL, and returns
// the upload_id for a subsequent POST /api/v1/deploys with
// source_type=archive. The file is deleted by the builder after unpack; the
// TTL is the backstop.
func (h *Handler) UploadArchive(c *gin.Context) {
	if h.uploads == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("uploads_disabled", "archive uploads are not available"))
		return
	}
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}

	if c.Request.ContentLength > h.maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, errResponse("upload_too_large",
			"archive exceeds the maximum size of "+strconv.FormatInt(h.maxUploadBytes, 10)+" bytes"))
		return
	}

	// The body is streamed to disk rather than read into memory. It used to be
	// io.ReadAll into a []byte that was then handed to Redis, so a 50 MB
	// archive existed twice in this process before it existed anywhere useful.
	body := http.MaxBytesReader(c.Writer, c.Request.Body, h.maxUploadBytes)

	// The gzip magic is checked on the first two bytes as they go past, so a
	// body that is not an archive is refused without writing the rest of it.
	// Full validation happens at unpack time.
	magic := make([]byte, 2)
	if _, err := io.ReadFull(body, magic); err != nil || magic[0] != 0x1f || magic[1] != 0x8b {
		c.JSON(http.StatusBadRequest, errResponse("invalid_archive", "body must be a gzip-compressed tar archive"))
		return
	}

	uploadID, size, err := h.uploads.Put(c.Request.Context(), userID.String(),
		io.MultiReader(bytes.NewReader(magic), body), h.maxUploadBytes)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.Is(err, uploads.ErrTooLarge) || errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, errResponse("upload_too_large",
				"archive exceeds the maximum size of "+strconv.FormatInt(h.maxUploadBytes, 10)+" bytes"))
			return
		}
		h.log.Error("store upload failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to store upload"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"upload_id":      uploadID,
		"size_bytes":     size,
		"expires_in_sec": int(h.uploadTTL.Seconds()),
	})
}

// GetDeploy handles GET /api/v1/deploys/:id — returns the deploy row,
// scoped to the calling user.
func (h *Handler) GetDeploy(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	d, err := h.repo.Get(c.Request.Context(), deployID)
	if err != nil {
		c.JSON(http.StatusNotFound, errResponse("not_found", "deploy not found"))
		return
	}
	if d.UserID != userID {
		c.JSON(http.StatusForbidden, errResponse("forbidden", "deploy belongs to a different user"))
		return
	}

	// The saga rides along rather than living behind its own endpoint: every
	// caller that wants one wants the other, and this is the screen where "why
	// is it stuck" gets answered.
	//
	// It is embedded so the deploy's own fields stay exactly where they were.
	// A nested {"deploy": …} would have been tidier and would have broken every
	// existing client for a field most of them do not read. A missing saga is
	// omitted rather than an error: rows predating the orchestrator have none.
	body := deployDetailResponse{Deploy: *d}
	if s, err := h.repo.GetSaga(c.Request.Context(), deployID); err != nil {
		h.log.Warn("failed to read saga state for a deploy",
			zap.String("deploy_id", deployID.String()), zap.Error(err))
	} else {
		body.Saga = s
	}
	c.JSON(http.StatusOK, body)
}

// deployDetailResponse is the deploy plus its orchestration state.
type deployDetailResponse struct {
	Deploy
	Saga *SagaView `json:"saga,omitempty"`
}

// ListDeploys handles GET /api/v1/deploys?limit=&offset= — paginated list
// of the calling user's deploys, ordered by created_at desc.
func (h *Handler) ListDeploys(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	deploys, err := h.repo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("list deploys failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list deploys"))
		return
	}
	if deploys == nil {
		deploys = []Deploy{}
	}
	c.JSON(http.StatusOK, gin.H{"deploys": deploys, "limit": limit, "offset": offset})
}

// DeleteDeploy handles DELETE /api/v1/deploys/:id - stops the runtime if needed
// and marks the deploy deleted for the calling user.
func (h *Handler) DeleteDeploy(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	d, err := h.repo.Get(c.Request.Context(), deployID)
	if err != nil {
		c.JSON(http.StatusNotFound, errResponse("not_found", "deploy not found"))
		return
	}
	if d.UserID != userID {
		c.JSON(http.StatusForbidden, errResponse("forbidden", "deploy belongs to a different user"))
		return
	}
	if d.Status == "deleted" {
		c.Status(http.StatusNoContent)
		return
	}

	needsRuntime := (d.ContainerID != nil && *d.ContainerID != "") ||
		(d.ImageRef != nil && *d.ImageRef != "" && d.ImageDeletedAt == nil)
	if needsRuntime && h.runner == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("runner_unavailable", "runner client is not configured"))
		return
	}

	if d.ContainerID != nil && *d.ContainerID != "" {
		if err := h.runner.Stop(c.Request.Context(), deployID.String(), *d.ContainerID); err != nil {
			h.log.Error("failed to stop deploy runtime",
				zap.String("deploy_id", deployID.String()),
				zap.String("container_id", *d.ContainerID),
				zap.Error(err),
			)
			c.JSON(http.StatusBadGateway, errResponse("stop_failed", "failed to stop deploy runtime"))
			return
		}
	}

	if err := h.repo.MarkDeleted(c.Request.Context(), deployID); err != nil {
		h.log.Error("failed to mark deploy deleted",
			zap.String("deploy_id", deployID.String()),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to delete deploy"))
		return
	}

	// Deleting is what releases the image; stopping no longer does, because a
	// stopped deploy has to stay startable. This runs after MarkDeleted and is
	// best-effort on purpose: the row is 'deleted' now, so it is already in the
	// image sweep's queue and a Docker failure here is retried within a tick
	// rather than turning a completed deletion into a 502.
	if d.ImageRef != nil && *d.ImageRef != "" && d.ImageDeletedAt == nil {
		if err := h.runner.RemoveImage(c.Request.Context(), deployID.String(), *d.ImageRef); err != nil {
			h.log.Warn("deploy image cleanup deferred to the watchdog",
				zap.String("deploy_id", deployID.String()),
				zap.String("image_ref", *d.ImageRef),
				zap.Error(err),
			)
		}
	}

	c.Status(http.StatusNoContent)
}

// StopDeploy handles POST /api/v1/deploys/:id/stop — tears the container down
// and leaves everything else alone.
//
// It exists because stopping and deleting are different operations and the
// panel had only one. "Остановить" called DELETE, which marks the deploy
// deleted and releases its image, so stopping a site destroyed the artifact
// that StartDeploy needs — the start button would have had nothing to start,
// on a deploy the operator only meant to pause.
func (h *Handler) StopDeploy(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	d, err := h.repo.Get(c.Request.Context(), deployID)
	if err != nil {
		c.JSON(http.StatusNotFound, errResponse("not_found", "deploy not found"))
		return
	}
	if d.UserID != userID {
		c.JSON(http.StatusForbidden, errResponse("forbidden", "deploy belongs to a different user"))
		return
	}
	if d.Status == "stopped" {
		c.JSON(http.StatusOK, gin.H{"status": "stopped"})
		return
	}
	if d.Status != "running" || d.ContainerID == nil || *d.ContainerID == "" {
		c.JSON(http.StatusConflict, errResponse("not_running", "only a running deploy can be stopped"))
		return
	}
	if h.runner == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("runner_unavailable", "runner client is not configured"))
		return
	}

	// Tearing a container down outlives the request that asked for it.
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := h.runner.Stop(stopCtx, deployID.String(), *d.ContainerID); err != nil {
		h.log.Error("failed to stop deploy",
			zap.String("deploy_id", deployID.String()),
			zap.String("container_id", *d.ContainerID),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, errResponse("stop_failed", "failed to stop the deploy runtime"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// StartDeploy handles POST /api/v1/deploys/:id/start — brings a stopped
// deploy back up from the image it was already built into.
//
// This is not a rebuild and deliberately does not go through the saga. The
// artifact exists, the port the build detected is on the saga row, and the
// runtime's own liveness probe still decides what "running" means — so the
// work is one container run, and routing it through the build queue would
// mean re-cloning a repository to produce an image that is already on disk.
//
// It is the reason FindImagesPendingCleanup skips 'stopped'. If that ever
// changes back, this endpoint starts answering image_reclaimed for every
// deploy and the button becomes decoration.
func (h *Handler) StartDeploy(c *gin.Context) {
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}
	if h.runner == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("runner_unavailable", "runner client is not configured"))
		return
	}

	target, err := h.repo.BeginRestart(c.Request.Context(), deployID, h.restartPort)
	switch {
	case errors.Is(err, ErrDeployNotFound):
		c.JSON(http.StatusNotFound, errResponse("not_found", "deploy not found"))
		return
	case errors.Is(err, ErrNotStopped):
		c.JSON(http.StatusConflict, errResponse("not_stopped",
			"only a stopped deploy can be started"))
		return
	case errors.Is(err, ErrNotRestartable):
		c.JSON(http.StatusConflict, errResponse("image_reclaimed",
			"the build artifact has been reclaimed; deploy the project again to rebuild it"))
		return
	case err != nil:
		h.log.Error("failed to claim deploy for restart",
			zap.String("deploy_id", deployID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to start deploy"))
		return
	}

	// The ownership check comes after the claim because the claim is what
	// makes the read consistent; a refusal here still has to release it.
	if target.UserID != userID {
		h.abandonRestart(deployID, "")
		c.JSON(http.StatusForbidden, errResponse("forbidden", "deploy belongs to a different user"))
		return
	}

	// Starting a container outlives the request that asked for it: a browser
	// that navigates away must not leave a running container with the row
	// still saying 'provisioning'.
	runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := h.runner.Deploy(runCtx, saga.DeployRequest{
		DeployID:       deployID.String(),
		UserID:         userID.String(),
		ImageRef:       target.ImageRef,
		Port:           target.Port,
		TTLMinutes:     h.restartTTLMin,
		IdempotencyKey: deployID.String() + ":start",
	})
	if err != nil {
		reason := "start failed"
		var statusErr *saga.StatusError
		if errors.As(err, &statusErr) {
			reason = statusErr.UserReason()
		}
		h.abandonRestart(deployID, reason)
		h.log.Error("failed to start stopped deploy",
			zap.String("deploy_id", deployID.String()),
			zap.String("image_ref", target.ImageRef),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, errResponse("start_failed", reason))
		return
	}

	h.log.Info("stopped deploy started from its existing image",
		zap.String("deploy_id", deployID.String()),
		zap.String("container_id", resp.ContainerID),
		zap.Int("port", target.Port),
	)
	c.JSON(http.StatusOK, gin.H{
		"status":       "running",
		"container_id": resp.ContainerID,
		"endpoint_url": resp.EndpointURL,
	})
}

// abandonRestart releases a claim the runtime did not take up. It uses its own
// context because the request's may already be the reason we are here.
func (h *Handler) abandonRestart(deployID uuid.UUID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.repo.AbandonRestart(ctx, deployID, reason); err != nil {
		h.log.Error("failed to release a restart claim; the deploy is stuck in provisioning",
			zap.String("deploy_id", deployID.String()), zap.Error(err))
	}
}

// GetLogs handles GET /api/v1/deploys/:id/logs?since=&limit= — returns
// buffered build and saga logs: from memory while the deploy is live, and from
// the archived tail once it is not.
// Pass the response's `next_since` as `since` on the following request to
// page through new entries; an empty `since` returns the full history.
func (h *Handler) GetLogs(c *gin.Context) {
	if h.logReader == nil {
		c.JSON(http.StatusServiceUnavailable, errResponse("logs_disabled", "log history is not available"))
		return
	}
	userID, err := userIDFromHeader(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", err.Error()))
		return
	}
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	d, err := h.repo.Get(c.Request.Context(), deployID)
	if err != nil {
		c.JSON(http.StatusNotFound, errResponse("not_found", "deploy not found"))
		return
	}
	if d.UserID != userID {
		c.JSON(http.StatusForbidden, errResponse("forbidden", "deploy belongs to a different user"))
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	since := c.Query("since")

	entries, next, err := h.logReader.Read(c.Request.Context(), deployID.String(), since, limit)
	if err != nil {
		h.log.Error("read log history failed", zap.String("deploy_id", deployID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to read log history"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "next_since": next})
}

// userIDFromHeader extracts the api-gateway-injected X-User-ID header.
func userIDFromHeader(c *gin.Context) (uuid.UUID, error) {
	raw := c.GetHeader("X-User-ID")
	if raw == "" {
		return uuid.Nil, errMissingUser
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errMissingUser
	}
	return id, nil
}

// errMissingUser is returned when X-User-ID is missing or invalid.
var errMissingUser = httpError("X-User-ID header missing or invalid")

type httpError string

func (e httpError) Error() string { return string(e) }

// errResponse builds a standard JSON error body.
func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}

// DeployInfo is the response shape for GET /internal/deploys/:id.
// All four fields are always serialized — callers (runner-svc) rely on
// presence-vs-empty distinction (e.g. empty image_ref must reach the
// validator as "", not be absent).
type DeployInfo struct {
	DeployID    string `json:"deploy_id"`
	UserID      string `json:"user_id"`
	Status      string `json:"status"`
	ImageRef    string `json:"image_ref"`
	ContainerID string `json:"container_id"`
}

// GetDeployInternal handles GET /internal/deploys/:id — webhook-secret
// protected, returns the minimal DeployInfo for service-to-service
// validation (runner-svc cross-checks image_ref / user_id / status
// before launching a container).
func (h *Handler) GetDeployInternal(c *gin.Context) {
	deployID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "id must be a valid UUID"))
		return
	}

	d, err := h.repo.Get(c.Request.Context(), deployID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "deploy not found"})
			return
		}
		h.log.Error("failed to get deploy for internal lookup",
			zap.String("deploy_id", deployID.String()),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to get deploy"))
		return
	}

	info := DeployInfo{
		DeployID: d.ID.String(),
		UserID:   d.UserID.String(),
		Status:   d.Status,
	}
	if d.ImageRef != nil {
		info.ImageRef = *d.ImageRef
	}
	if d.ContainerID != nil {
		info.ContainerID = *d.ContainerID
	}
	c.JSON(http.StatusOK, info)
}
