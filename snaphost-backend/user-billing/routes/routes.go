// Package routes registers all HTTP routes for the user-billing service.
package routes

import (
	"github.com/gin-gonic/gin"

	"snaphost/user-billing/internal/account"
	"snaphost/user-billing/internal/admin"
	"snaphost/user-billing/internal/apikey"
	"snaphost/user-billing/internal/deploy"
	"snaphost/user-billing/internal/domain"
)

// Register sets up all public and internal routes on the Gin engine.
// Public routes sit behind api-gateway which handles JWT authentication
// and forwards X-User-ID. Internal routes are protected by the shared
// webhook secret.
// domainHandler and tlsHandler may be nil when custom domains are not
// configured.
func Register(r *gin.Engine, accountHandler *account.Handler, deployHandler *deploy.Handler, apikeyHandler *apikey.Handler, domainHandler *domain.Handler, tlsHandler *domain.TLSHandler, adminHandler *admin.Handler, webhookSecret string) {
	// Public routes — api-gateway sets X-User-ID after JWT verification.
	api := r.Group("/api/v1")
	{
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

	// Internal routes — protected by webhook secret, never exposed through api-gateway.
	internal := r.Group("/internal")
	internal.Use(account.WebhookSecretMiddleware(webhookSecret))
	{
		// Records the account behind a user_id. Called by the identity
		// provider's signup webhook; it is the only path an email takes into
		// this database.
		internal.POST("/users", accountHandler.Create)

		// API-key verification called by api-gateway to resolve a key to a user.
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
