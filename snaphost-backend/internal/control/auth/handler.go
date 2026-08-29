package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CookieOptions controls how the session cookie is written.
type CookieOptions struct {
	// Secure forces the Secure attribute on or off. Nil derives it from the
	// request, which is what a self-hosted install needs: the operator reaches
	// a fresh box over plain HTTP on an IP address to log in for the first time
	// and put a certificate on it, and a cookie the browser refuses to send
	// makes that impossible. Once an edge terminates TLS the derivation turns
	// it on by itself.
	Secure *bool
}

// Handler serves the session endpoints.
type Handler struct {
	svc     *Service
	cookies CookieOptions
	limiter *LoginLimiter
	log     *zap.Logger
}

// NewHandler creates the auth HTTP handler.
func NewHandler(svc *Service, cookies CookieOptions, log *zap.Logger) *Handler {
	return &Handler{svc: svc, cookies: cookies, limiter: NewLoginLimiter(0, 0), log: log}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

// Login exchanges a password for a session cookie.
//
// It is the one public route left on this platform, which is why it carries the
// only unauthenticated password check: everything else either presents the
// cookie it returns or an sk_ key.
func (h *Handler) Login(c *gin.Context) {
	// Checked before the body is even parsed, so an address that is over the
	// limit costs nothing — including the argon2 hash, which is the expensive
	// half of answering a wrong password.
	addr := c.ClientIP()
	if ok, retryAfter := h.limiter.Allow(addr); !ok {
		h.log.Warn("login refused: too many failed attempts", zap.String("client_ip", addr))
		tooManyAttempts(c, retryAfter)
		return
	}

	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", "email and password are required"))
		return
	}

	token, session, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// Only a wrong credential counts. An internal error is this
			// platform's fault, and holding it against the client would turn a
			// database hiccup into a lockout.
			h.limiter.Fail(addr)
			c.JSON(http.StatusUnauthorized, errResponse("invalid_credentials", "email or password is wrong"))
			return
		}
		h.log.Error("login failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "login failed"))
		return
	}
	h.limiter.Succeed(addr)

	h.setSessionCookie(c, token, int(h.svc.TTL().Seconds()))
	c.JSON(http.StatusOK, identityBody(session))
}

// Logout revokes the presented session and clears the cookie. It answers 204
// whether or not a session was found: the caller asked to be logged out, and
// they are.
func (h *Handler) Logout(c *gin.Context) {
	token, _ := c.Cookie(CookieName)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		h.log.Error("logout failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "logout failed"))
		return
	}
	h.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

// Me reports who the request is authenticated as.
//
// It reads the headers the gateway middleware wrote from a verified session or
// API key rather than re-reading the cookie, so it answers for both credential
// kinds and cannot disagree with what authorisation decided.
func (h *Handler) Me(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-ID"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("unauthorized", "no identity on this request"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id": userID.String(),
		"email":   c.GetHeader("X-User-Email"),
		"role":    c.GetHeader("X-User-Role"),
	})
}

// ChangePassword rotates the password and revokes every other session.
func (h *Handler) ChangePassword(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-ID"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, errResponse("unauthorized", "no identity on this request"))
		return
	}

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse("invalid_body", "current_password and new_password are required"))
		return
	}

	token, _ := c.Cookie(CookieName)
	err = h.svc.ChangePassword(c.Request.Context(), userID, token, req.CurrentPassword, req.NewPassword)
	switch {
	case err == nil:
	case errors.Is(err, ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, errResponse("invalid_credentials", "current password is wrong"))
		return
	case errors.Is(err, ErrPasswordTooShort), errors.Is(err, ErrPasswordTooLong):
		c.JSON(http.StatusBadRequest, errResponse("weak_password", err.Error()))
		return
	case errors.Is(err, ErrNoAccount):
		c.JSON(http.StatusUnauthorized, errResponse("unauthorized", "no such account"))
		return
	default:
		h.log.Error("password change failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errResponse("internal_error", "password change failed"))
		return
	}

	h.log.Info("operator password changed", zap.String("user_id", userID.String()))
	c.Status(http.StatusNoContent)
}

// Register maps the session routes. Only login is public; the rest read an
// identity the middleware has already established.
func (h *Handler) Register(api *gin.RouterGroup) {
	api.POST("/auth/login", h.Login)
	api.POST("/auth/logout", h.Logout)
	api.GET("/auth/me", h.Me)
	api.POST("/auth/password", h.ChangePassword)
}

func (h *Handler) setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:  CookieName,
		Value: value,
		Path:  "/",
		// HttpOnly: the panel never needs to read this from JavaScript, and a
		// token script can read is a token an injected script can exfiltrate.
		HttpOnly: true,
		Secure:   h.secure(c),
		// Lax rather than Strict: it still withholds the cookie from every
		// cross-site POST and DELETE, which is the CSRF protection this API
		// relies on, while allowing the operator to follow a link into the
		// panel without landing on a login screen.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

func (h *Handler) clearSessionCookie(c *gin.Context) {
	h.setSessionCookie(c, "", -1)
}

// secure decides the cookie's Secure attribute. X-Forwarded-Proto is trusted
// here because the only thing it can change is whether our own cookie insists
// on TLS: an attacker able to forge it into "http" is already able to strip the
// TLS the attribute would have demanded.
func (h *Handler) secure(c *gin.Context) bool {
	if h.cookies.Secure != nil {
		return *h.cookies.Secure
	}
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

func identityBody(s *Session) gin.H {
	return gin.H{
		"user_id":    s.UserID.String(),
		"email":      s.Email,
		"role":       s.Role,
		"expires_at": s.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

func errResponse(code, message string) gin.H {
	return gin.H{"error": code, "message": message}
}
