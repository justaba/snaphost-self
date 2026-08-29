package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"snaphost/internal/shared"
)

type contextKey string

const (
	CtxKeyUserID    = contextKey("userID")
	CtxKeyUserEmail = contextKey("userEmail")
	CtxKeyUserRole  = contextKey("userRole")
)

// PublicRoutes defines routes that skip authentication and authorization.
// Both Auth and Casbin consult it, so a route added here is exempt from both.
//
// It used to name auth/register and auth/login, and neither route existed:
// registration went straight from the browser to Supabase, and the gateway
// carried entries for endpoints it had never served. Login is a real route
// now, and it is the only public one — everything else presents either the
// cookie login hands out or an sk_ API key.
var PublicRoutes = map[string]bool{
	"POST:/api/v1/auth/login": true,
	"GET:/health":             true,
	"GET:/metrics":            true,
}

// Identity is who a request is from, as established by a credential this
// platform issued. It is the whole result of authentication: Enrich forwards
// exactly these three values and nothing else downstream is trusted.
type Identity struct {
	UserID string
	Email  string
	Role   string
}

// SessionVerifier resolves a session token to the identity behind it.
//
// An interface for the same reason KeyVerifier is one: it keeps the middleware
// testable without a database, and the implementation is a direct repository
// read rather than a call to an identity provider on the internet.
type SessionVerifier interface {
	Verify(ctx context.Context, token string) (Identity, error)
}

// Auth establishes who is making a request, from one of two credentials:
//
//   - an sk_ API key in the Authorization header, which is how every
//     non-browser client authenticates — the MCP server, the editor extension,
//     a curl in a script;
//   - the session cookie, which is how the panel does, because a browser will
//     not attach an Authorization header to a navigation.
//
// There is no third. The JWT this replaced was signed by Supabase and verified
// against a JWKS fetched over the internet at startup, which made logging into
// a self-hosted panel depend on an external service being reachable.
func Auth(sessions SessionVerifier, apiKeys KeyVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if PublicRoutes[c.Request.Method+":"+c.FullPath()] {
			c.Next()
			return
		}

		// A presented Authorization header is answered on its own terms: if it
		// is there and wrong, falling through to the cookie would let a client
		// with a revoked key act as whoever last logged in on that browser.
		if header := c.GetHeader("Authorization"); header != "" {
			identity, reason := identityFromBearer(c.Request.Context(), header, apiKeys)
			if reason != "" {
				unauthorized(c, reason)
				return
			}
			setIdentity(c, identity)
			c.Next()
			return
		}

		token, err := c.Cookie(shared.SessionCookie)
		if err != nil || token == "" {
			unauthorized(c, "missing_credentials")
			return
		}
		identity, err := sessions.Verify(c.Request.Context(), token)
		if err != nil {
			unauthorized(c, "invalid_session")
			return
		}

		setIdentity(c, identity)
		c.Next()
	}
}

// identityFromBearer resolves an Authorization header. The returned reason is
// empty on success and is the value surfaced to the client otherwise.
func identityFromBearer(ctx context.Context, header string, apiKeys KeyVerifier) (Identity, string) {
	scheme, credential, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return Identity{}, "invalid_authorization"
	}
	credential = strings.TrimSpace(credential)

	// Anything that is not an API key is refused rather than guessed at. The
	// only other bearer this ever accepted was a Supabase JWT.
	if !IsAPIKey(credential) {
		return Identity{}, "unsupported_credential"
	}
	if apiKeys == nil {
		return Identity{}, "api_key_unsupported"
	}

	userID, err := apiKeys.Verify(ctx, credential)
	if err != nil {
		return Identity{}, "invalid_api_key"
	}

	// An API key authenticates as an ordinary user whatever role its owner
	// holds. The operator console is reachable from a browser session only:
	// a long-lived key stored in an editor's settings should not be able to
	// read every account on the host.
	return Identity{UserID: userID, Role: "user"}, ""
}

func setIdentity(c *gin.Context, identity Identity) {
	c.Set(string(CtxKeyUserID), identity.UserID)
	c.Set(string(CtxKeyUserEmail), identity.Email)
	c.Set(string(CtxKeyUserRole), identity.Role)
}

func unauthorized(c *gin.Context, reason string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": reason})
}
