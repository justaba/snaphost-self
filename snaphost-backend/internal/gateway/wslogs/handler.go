// Package wslogs implements a WebSocket endpoint that streams deploy logs to
// the browser.
//
// It is registered before the standard middleware chain and authenticates
// itself, because a browser cannot put an Authorization header on a WebSocket
// handshake. That used to mean a Supabase JWT in a `token` query parameter;
// it now means the session cookie, which the browser attaches by itself.
//
// Moving to a cookie brings a hole CORS does not cover. A WebSocket handshake
// is not subject to the same-origin policy, so any page the operator visits
// could open one to the panel and receive it authenticated — cross-site
// WebSocket hijacking. The Origin check below is what closes it, and it is
// load-bearing rather than defensive: with the old query-parameter token, an
// attacking page had nothing to send.
package wslogs

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/internal/gateway/middleware"
	"snaphost/internal/shared"
)

const (
	pingPeriod = 30 * time.Second
	readWait   = 70 * time.Second
	writeWait  = 10 * time.Second
)

// DeployOwner reports which account a deploy belongs to.
//
// The stream had no ownership check at all: any valid token could subscribe to
// any deploy's logs by id. With one operator that was unreachable, and every
// table here keeps its user_id precisely so a second operator or a service
// account stays isolated — an authorisation that only holds while there is one
// account is not one.
type DeployOwner interface {
	Owner(ctx context.Context, deployID uuid.UUID) (string, error)
}

// Handler upgrades the connection, authenticates it from the session cookie,
// checks that the deploy belongs to the caller, then bridges Redis pub/sub
// messages on logs:{deploy_id} to the client until either side disconnects.
func Handler(
	rdb *redis.Client,
	sessions middleware.SessionVerifier,
	deploys DeployOwner,
	allowedOrigins []string,
	logger *zap.Logger,
) gin.HandlerFunc {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return originAllowed(r, allowedOrigins) },
	}

	return func(c *gin.Context) {
		// Origin first, before anything is looked up. The upgrader would check
		// it too, but only once the handler had already answered 404 or 403 for
		// the deploy id — which tells a cross-site page whether a deploy exists
		// even though its socket never opens.
		if !originAllowed(c.Request, allowedOrigins) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "reason": "origin_not_allowed"})
			return
		}

		deployID, err := uuid.Parse(strings.Trim(c.Param("id"), "/"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_deploy_id"})
			return
		}

		token, err := c.Cookie(shared.SessionCookie)
		if err != nil || token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "missing_session"})
			return
		}
		identity, err := sessions.Verify(c.Request.Context(), token)
		if err != nil {
			logger.Debug("ws auth failed", zap.Error(err))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "invalid_session"})
			return
		}

		owner, err := deploys.Owner(c.Request.Context(), deployID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if owner != identity.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logger.Warn("ws upgrade failed", zap.Error(err))
			return
		}
		defer conn.Close()

		stream(c.Request.Context(), conn, rdb, deployID.String(), logger)
	}
}

// originAllowed decides whether a handshake may proceed.
//
// No Origin at all is allowed: that is a non-browser client, which cannot be
// tricked into sending a cookie it does not have. A browser always sends one,
// and it must either match the host being asked or be a configured origin —
// the panel is served by this binary, so same-origin is the normal case and the
// list exists for a separately hosted development server.
func originAllowed(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimRight(candidate, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

// stream subscribes to logs:{deployID} and pumps messages to the WS
// client until the parent context is cancelled, the client disconnects,
// or the subscription closes.
func stream(ctx context.Context, conn *websocket.Conn, rdb *redis.Client, deployID string, logger *zap.Logger) {
	channel := "logs:" + deployID
	sub := rdb.Subscribe(ctx, channel)
	defer sub.Close()

	// Wait for the SUBSCRIBE confirmation so we don't miss messages
	// between Subscribe returning and the goroutine being attached.
	if _, err := sub.Receive(ctx); err != nil {
		logger.Warn("ws subscribe failed", zap.String("channel", channel), zap.Error(err))
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(readWait))
		return nil
	})

	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()

	msgCh := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clientGone:
			return
		case <-pingTicker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				if !errors.Is(err, websocket.ErrCloseSent) {
					logger.Debug("ws write failed", zap.Error(err))
				}
				return
			}
		}
	}
}
