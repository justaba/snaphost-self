package middleware

import (
	"net/http"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
)

// Casbin middleware for RBAC authorization
func Casbin(enforcer *casbin.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		lookupKey := c.Request.Method + ":" + c.FullPath()
		if PublicRoutes[lookupKey] {
			c.Next()
			return
		}

		// A missing role, or one stored as something other than a string,
		// enforces as "guest" — the narrowest role there is. A wrong type must
		// never widen a permission, and must not panic on a request whose
		// authorisation has not been decided yet.
		raw, exists := c.Get(string(CtxKeyUserRole))
		role, isString := raw.(string)
		if !exists || !isString {
			role = "guest"
		}

		// Use c.Request.URL.Path for enforcement against policies with :id
		allowed, err := enforcer.Enforce(role, c.Request.URL.Path, c.Request.Method)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization_error"})
			return
		}

		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		c.Next()
	}
}
