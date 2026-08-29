// Package routes registers all HTTP routes for the user-billing service.
package routes

import (
	"github.com/gin-gonic/gin"

	"snaphost/internal/control/admin"
	"snaphost/internal/control/apikey"
	"snaphost/internal/control/auth"
	"snaphost/internal/control/deploy"
	"snaphost/internal/control/domain"
	"snaphost/internal/shared"
)

// Register sets up the public API surface. Handlers read the identity from
// the X-User-ID header the gateway middleware writes from a verified session
// or API key.
//
// domainHandler may be nil when custom domains are not configured.
func Register(r *gin.Engine, authHandler *auth.Handler, deployHandler *deploy.Handler, apikeyHandler *apikey.Handler, domainHandler *domain.Handler, adminHandler *admin.Handler) {
	// The identity headers are written by Enrich, which runs last in the
	// middleware chain and deletes them when the request is unauthenticated.
	api := r.Group("/api/v1")
	{
		// The operator's session. Login is the one route in this file that runs
		// unauthenticated — it is in middleware.PublicRoutes, and adding a
		// route here without adding it there means Casbin refuses it for the
		// "guest" role every time.
		if authHandler != nil {
			authHandler.Register(api)
		}

		// API key management for non-browser clients.
		api.POST("/keys", apikeyHandler.CreateKey)
		api.GET("/keys", apikeyHandler.ListKeys)
		api.DELETE("/keys/:id", apikeyHandler.RevokeKey)

		// Deploy lifecycle — saga-driven.
		api.POST("/deploys/upload", deployHandler.UploadArchive)
		api.POST("/deploys", deployHandler.CreateDeploy)
		api.GET("/deploys", deployHandler.ListDeploys)
		api.GET("/deploys/:id", deployHandler.GetDeploy)
		api.DELETE("/deploys/:id", deployHandler.DeleteDeploy)
		api.GET("/deploys/:id/logs", deployHandler.GetLogs)

		// Custom domains — the alias layer. Attach returns the DNS records
		// to publish; nothing routes until verification succeeds.
		if domainHandler != nil {
			api.POST("/domains", domainHandler.Attach)
			api.GET("/domains", domainHandler.List)
			api.DELETE("/domains/:id", domainHandler.Detach)
			api.POST("/domains/:id/target", domainHandler.SetTarget)
		}
	}

	// Operator read surface. Authorised twice: api-gateway's Casbin policy
	// covers these paths for the admin role, and RequireAdmin re-checks the
	// forwarded role here so a missing policy line cannot expose every
	// account's data. Read-only by design — operator actions get their own
	// pass with an audit trail.
	if adminHandler != nil {
		adm := r.Group("/api/v1/admin", admin.RequireAdmin())
		{
			adm.GET("/overview", adminHandler.Overview)

			adm.GET("/users", adminHandler.ListUsers)
			adm.GET("/users/:id", adminHandler.GetUser)
			adm.GET("/users/:id/deploys", adminHandler.UserDeploys)
			adm.GET("/users/:id/domains", adminHandler.UserDomains)
			adm.GET("/users/:id/projects", adminHandler.UserProjects)
			adm.GET("/users/:id/keys", adminHandler.UserKeys)

			adm.GET("/deploys", adminHandler.ListDeploys)
			adm.GET("/deploys/:id", adminHandler.GetDeploy)

			adm.GET("/domains", adminHandler.ListDomains)
		}
	}
}

// RegisterInternal sets up the routes authenticated by the shared webhook
// secret rather than a user token.
//
// It is registered separately from the public surface, and before the Auth and
// Casbin middleware, because these callers present a secret rather than a
// session — running them through user authentication would reject every one.
// The split used to be enforced by them living in a different process.
func RegisterInternal(r *gin.Engine, deployHandler *deploy.Handler, apikeyHandler *apikey.Handler, tlsHandler *domain.TLSHandler, webhookSecret string) {
	// Internal routes — protected by webhook secret, never exposed publicly.
	//
	// POST /users used to live here: it recorded the account behind a user_id
	// and was called by Supabase's signup webhook. Both are gone, and with them
	// the last caller of that endpoint — an account is created by the operator
	// bootstrap now, with a password, which that endpoint could never set.
	internal := r.Group("/internal")
	internal.Use(shared.WebhookAuth(webhookSecret))
	{
		// API-key verification, kept for a caller outside this process.
		internal.POST("/keys/verify", apikeyHandler.VerifyKey)

		// Deploy lifecycle endpoints called by runner-svc / builder-svc.
		internal.POST("/deploys/:id/status", deployHandler.UpdateStatus)
		internal.POST("/deploys/:id/running", deployHandler.SetRunning)
		internal.GET("/deploys/expired", deployHandler.ListExpired)
		internal.GET("/deploys/:id", deployHandler.GetDeployInternal)
		internal.GET("/routes", deployHandler.LookupRouteByHost)

		// On-demand TLS gate for the custom-domain edge (ADR 0007). Caddy asks
		// before issuing a certificate for a hostname; only a verified domain
		// is authorised.
		if tlsHandler != nil {
			internal.GET("/tls/authorize", tlsHandler.Authorize)
		}
	}
}
