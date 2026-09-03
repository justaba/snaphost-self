package domain

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Store is the data access surface the handler needs; *Repository satisfies it.
type Store interface {
	Create(ctx context.Context, userID, projectID uuid.UUID, host, token string, target *uuid.UUID) (*Domain, error)
	Get(ctx context.Context, userID, id uuid.UUID) (*Domain, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Domain, error)
	CountActiveByUser(ctx context.Context, userID uuid.UUID) (int, error)
	Revoke(ctx context.Context, userID, id uuid.UUID) error
	SetTarget(ctx context.Context, userID, id, deployID uuid.UUID) (*Domain, error)
	OwnsProject(ctx context.Context, userID, projectID uuid.UUID) (bool, error)
	ProjectOfDeploy(ctx context.Context, userID, deployID uuid.UUID) (uuid.UUID, error)
}

// Limiter bounds how often one user may attach a domain. Each attach costs
// DNS lookups and, on an on-demand-TLS edge, a certificate issuance attempt,
// so this is a cost gate rather than a correctness one. A nil Limiter disables
// the bound.
type Limiter interface {
	Allow(ctx context.Context, userID string) (bool, error)
}

// Config holds the policy knobs the handler enforces.
type Config struct {
	// PlatformSuffix is our own runtime domain suffix; hosts under it are
	// assigned by the platform and cannot be attached.
	PlatformSuffix string
	// ReservedDomains are the platform's other own zones — the control-plane
	// domain the dashboard and API answer on, most importantly. User content
	// lives on a different registrable domain than sessions do, and that
	// separation is worth defending at attach time too.
	ReservedDomains []string
	// MaxPerUser is the per-tier domain count. Free tier is one.
	MaxPerUser int
	// RequireIdentity gates attach on a payment-verified account. Free
	// custom domains attract phishing and spam and the cost lands on the
	// reputation of our edge address for every other user — abuse, not
	// cost, is what this gate is for. Payment collection does not exist
	// yet, so this ships default-off with manual abuse review; flipping it
	// on refuses every attach until an identity signal is wired in.
	RequireIdentity bool
	// CNAMETarget and ARecordTarget are the published DNS targets a user
	// points their domain at. Empty means "not reserved yet" and attach is
	// refused: a target that later moves breaks every customer's site at
	// once, and only they can repair it (Task 16 P2).
	CNAMETarget   string
	ARecordTarget string
}

// reservedZones lists every domain a user may not attach: our runtime suffix
// plus the platform's own zones.
func (c Config) reservedZones() []string {
	zones := make([]string, 0, len(c.ReservedDomains)+1)
	if c.PlatformSuffix != "" {
		zones = append(zones, c.PlatformSuffix)
	}
	return append(zones, c.ReservedDomains...)
}

// Handler exposes the public custom-domain API.
type Handler struct {
	store   Store
	limiter Limiter
	cfg     Config
	log     *zap.Logger
}

// NewHandler builds the custom-domain handler. limiter may be nil.
func NewHandler(store Store, limiter Limiter, cfg Config, log *zap.Logger) *Handler {
	return &Handler{store: store, limiter: limiter, cfg: cfg, log: log}
}

type attachRequest struct {
	Domain string `json:"domain" binding:"required"`
	// Exactly one of ProjectID / DeployID identifies what to publish.
	// Naming a deploy also makes it the initial alias target.
	ProjectID string `json:"project_id"`
	DeployID  string `json:"deploy_id"`
}

type domainResponse struct {
	Domain
	// DNS carries the records the user has to create. It is returned on
	// attach and on every listing, because a user who lost the first
	// response otherwise has no way to recover the token.
	DNS dnsInstructions `json:"dns"`
}

type dnsInstructions struct {
	VerificationRecord string `json:"verification_record"`
	VerificationType   string `json:"verification_type"`
	VerificationValue  string `json:"verification_value"`
	CNAMETarget        string `json:"cname_target,omitempty"`
	ARecordTarget      string `json:"a_record_target,omitempty"`
	// ApexNote explains why a bare example.com may need the A record
	// instead of the CNAME — the most common attach failure.
	ApexNote string `json:"apex_note"`
}

const apexNote = "A bare domain (example.com) usually cannot hold a CNAME. Use the A record for the apex and the CNAME for www."

func (h *Handler) respond(d *Domain) domainResponse {
	return domainResponse{
		Domain: *d,
		DNS: dnsInstructions{
			VerificationRecord: d.ChallengeRecord(),
			VerificationType:   "TXT",
			VerificationValue:  d.VerificationToken,
			CNAMETarget:        h.cfg.CNAMETarget,
			ARecordTarget:      h.cfg.ARecordTarget,
			ApexNote:           apexNote,
		},
	}
}

// Attach handles POST /api/v1/domains.
func (h *Handler) Attach(c *gin.Context) {
	userID, ok := userIDFromHeader(c)
	if !ok {
		return
	}

	var req attachRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}

	if h.cfg.CNAMETarget == "" && h.cfg.ARecordTarget == "" {
		c.JSON(http.StatusServiceUnavailable, errResponse("edge_address_unreserved",
			"custom domains are not available yet: no stable public address has been published to point DNS at"))
		return
	}
	if h.cfg.RequireIdentity {
		// No identity signal exists to satisfy this yet; refusing is the
		// only honest answer while the gate is switched on.
		c.JSON(http.StatusForbidden, errResponse("identity_required",
			"attaching a domain requires a verified payment method on this account"))
		return
	}

	host := Normalize(req.Domain)
	if code, message := Validate(host, h.cfg.reservedZones()); code != "" {
		c.JSON(http.StatusBadRequest, errResponse(code, message))
		return
	}

	projectID, target, ok := h.resolveTarget(c, userID, req)
	if !ok {
		return
	}

	count, err := h.store.CountActiveByUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("count domains failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to check domain quota"))
		return
	}
	if h.cfg.MaxPerUser > 0 && count >= h.cfg.MaxPerUser {
		c.JSON(http.StatusForbidden, errResponse("domain_limit_reached",
			"this account may attach "+strconv.Itoa(h.cfg.MaxPerUser)+" custom domain(s); detach one first"))
		return
	}

	if h.limiter != nil {
		allowed, err := h.limiter.Allow(c.Request.Context(), userID.String())
		if err != nil {
			h.log.Warn("domain attach limiter failed", zap.Error(err))
		} else if !allowed {
			c.JSON(http.StatusTooManyRequests, errResponse("rate_limited",
				"too many domain attach attempts; try again later"))
			return
		}
	}

	token, err := NewToken()
	if err != nil {
		h.log.Error("generate verification token failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to create domain"))
		return
	}

	d, err := h.store.Create(c.Request.Context(), userID, projectID, host, token, target)
	if err != nil {
		if errors.Is(err, ErrDuplicate) {
			c.JSON(http.StatusConflict, errResponse("domain_taken",
				"this domain is already attached; detach it first or contact support if you own it"))
			return
		}
		h.log.Error("create domain failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to create domain"))
		return
	}

	c.JSON(http.StatusCreated, h.respond(d))
}

// resolveTarget maps the request onto a project and an optional initial alias
// target, verifying ownership of whichever the caller named.
func (h *Handler) resolveTarget(c *gin.Context, userID uuid.UUID, req attachRequest) (uuid.UUID, *uuid.UUID, bool) {
	switch {
	case req.DeployID != "":
		deployID, err := uuid.Parse(req.DeployID)
		if err != nil {
			c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "deploy_id must be a valid UUID"))
			return uuid.Nil, nil, false
		}
		projectID, err := h.store.ProjectOfDeploy(c.Request.Context(), userID, deployID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				c.JSON(http.StatusNotFound, errResponse("deploy_not_found", "deploy not found for this account"))
				return uuid.Nil, nil, false
			}
			h.log.Error("resolve project of deploy failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to resolve project"))
			return uuid.Nil, nil, false
		}
		return projectID, &deployID, true

	case req.ProjectID != "":
		projectID, err := uuid.Parse(req.ProjectID)
		if err != nil {
			c.JSON(http.StatusBadRequest, errResponse("invalid_project_id", "project_id must be a valid UUID"))
			return uuid.Nil, nil, false
		}
		owns, err := h.store.OwnsProject(c.Request.Context(), userID, projectID)
		if err != nil {
			h.log.Error("check project ownership failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to resolve project"))
			return uuid.Nil, nil, false
		}
		if !owns {
			c.JSON(http.StatusNotFound, errResponse("project_not_found", "project not found for this account"))
			return uuid.Nil, nil, false
		}
		return projectID, nil, true

	default:
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", "one of project_id or deploy_id is required"))
		return uuid.Nil, nil, false
	}
}

// List handles GET /api/v1/domains.
func (h *Handler) List(c *gin.Context) {
	userID, ok := userIDFromHeader(c)
	if !ok {
		return
	}
	domains, err := h.store.ListByUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list domains failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to list domains"))
		return
	}
	out := make([]domainResponse, 0, len(domains))
	for i := range domains {
		out = append(out, h.respond(&domains[i]))
	}
	c.JSON(http.StatusOK, gin.H{"domains": out, "limit": h.cfg.MaxPerUser})
}

// Detach handles DELETE /api/v1/domains/:id. The domain stops resolving on
// the next route lookup and its target deploy is unpinned, returning it to
// ordinary GC.
func (h *Handler) Detach(c *gin.Context) {
	userID, ok := userIDFromHeader(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_domain_id", "id must be a valid UUID"))
		return
	}
	if err := h.store.Revoke(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, errResponse("not_found", "domain not found"))
			return
		}
		h.log.Error("revoke domain failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to detach domain"))
		return
	}
	c.Status(http.StatusNoContent)
}

type setTargetRequest struct {
	DeployID string `json:"deploy_id" binding:"required"`
}

// SetTarget handles POST /api/v1/domains/:id/target — publish or roll back by
// moving the pointer. No rebuild and no new image: any running deploy of the
// same project is a valid target.
func (h *Handler) SetTarget(c *gin.Context) {
	userID, ok := userIDFromHeader(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_domain_id", "id must be a valid UUID"))
		return
	}
	var req setTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", err.Error()))
		return
	}
	deployID, err := uuid.Parse(req.DeployID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_deploy_id", "deploy_id must be a valid UUID"))
		return
	}

	d, err := h.store.SetTarget(c.Request.Context(), userID, id, deployID)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			c.JSON(http.StatusNotFound, errResponse("not_found", "domain not found"))
		case errors.Is(err, ErrInvalidTarget):
			c.JSON(http.StatusConflict, errResponse("invalid_target",
				"deploy must be a running deploy of the same project"))
		default:
			h.log.Error("set domain target failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, errResponse("internal_error", "failed to repoint domain"))
		}
		return
	}
	c.JSON(http.StatusOK, h.respond(d))
}

func userIDFromHeader(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-ID")
	id, err := uuid.Parse(raw)
	if raw == "" || err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("missing_user", "X-User-ID header missing or invalid"))
		return uuid.Nil, false
	}
	return id, true
}

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
