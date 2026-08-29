// Package wslogs implements a WebSocket endpoint that streams deploy logs
// from Redis pub/sub to the browser. Auth is via a Supabase JWT in the
// `token` query parameter (browsers cannot set Authorization on WS), and
// the handler is registered BEFORE the standard middleware chain so that
// JWT/Casbin do not double-validate.
package wslogs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/api-gateway/middleware"
)

const (
	pingPeriod = 30 * time.Second
	readWait   = 70 * time.Second
	writeWait  = 10 * time.Second
)

// Handler returns a gin handler that upgrades the connection, validates
// the token, then bridges Redis pub/sub messages on logs:{deploy_id}
// to the WebSocket client until either side disconnects.
func Handler(rdb *redis.Client, jwks *middleware.JWKSCache, logger *zap.Logger) gin.HandlerFunc {
	upgrader := websocket.Upgrader{
		// Local dev: allow cross-origin WS. Tighten for production.
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return func(c *gin.Context) {
		deployID, err := uuid.Parse(strings.Trim(c.Param("id"), "/"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_deploy_id"})
			return
		}

		token := c.Query("token")
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing_token"})
			return
		}
		if _, reason, err := middleware.VerifyToken(c.Request.Context(), jwks, token); err != nil {
			logger.Debug("ws auth failed", zap.String("reason", reason), zap.Error(err))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": reason})
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
