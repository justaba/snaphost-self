package middleware

import (
	"github.com/gin-gonic/gin"
)

const (
	HeaderXUserID    = "X-User-ID"
	HeaderXUserEmail = "X-User-Email"
	HeaderXUserRole  = "X-User-Role"
	HeaderXFwdFor    = "X-Forwarded-For"
)

// contextString reads a string out of the Gin context. A value stored under
// another type reads as absent, which is what callers here want: these headers
// are the identity downstream services trust, so anything unexpected must
// clear the header rather than forward an unpredictable value — and must not
// panic on the request path.
func contextString(c *gin.Context, key string) (string, bool) {
	raw, exists := c.Get(key)
	if !exists {
		return "", false
	}
	value, isString := raw.(string)
	return value, isString
}

// forwardContextHeader sets a header from the context, or deletes it when the
// context has nothing usable to put there.
func forwardContextHeader(c *gin.Context, header, contextKey string) {
	if value, ok := contextString(c, contextKey); ok {
		c.Request.Header.Set(header, value)
		return
	}
	c.Request.Header.Del(header)
}

// Enrich middleware sets standard headers for downstream services
func Enrich() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Overwrite user info headers from context, prevent spoofing
		if userID, ok := contextString(c, string(CtxKeyUserID)); ok {
			c.Request.Header.Set(HeaderXUserID, userID)
			forwardContextHeader(c, HeaderXUserEmail, string(CtxKeyUserEmail))
			forwardContextHeader(c, HeaderXUserRole, string(CtxKeyUserRole))
		} else {
			c.Request.Header.Del(HeaderXUserID)
			c.Request.Header.Del(HeaderXUserEmail)
			c.Request.Header.Del(HeaderXUserRole)
		}

		// Ensure request ID is passed down. Unlike the identity headers, an
		// absent one is left as the caller sent it rather than deleted: it is
		// a correlation id, not a claim about who is asking.
		if reqID, ok := contextString(c, ContextKeyRequestID); ok {
			c.Request.Header.Set(HeaderXRequestID, reqID)
		}

		// Ensure X-Forwarded-For is set
		if c.Request.Header.Get(HeaderXFwdFor) == "" {
			c.Request.Header.Set(HeaderXFwdFor, c.ClientIP())
		}

		c.Next()
	}
}
