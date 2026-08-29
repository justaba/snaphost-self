package proxy

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WebSocket proxies WebSocket connections
func WebSocket(target string, logger *zap.Logger) gin.HandlerFunc {
	parsedURL, err := url.Parse(target)
	if err != nil {
		logger.Fatal("Invalid WS proxy target", zap.String("target", target), zap.Error(err))
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return func(c *gin.Context) {
		// Upgrade the original connection
		clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logger.Error("WebSocket upgrade failed", zap.Error(err))
			return
		}
		defer clientConn.Close()

		// Dial target WS URL
		targetURL := *parsedURL
		if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
			targetURL.Scheme = "wss"
		} else {
			targetURL.Scheme = strings.Replace(targetURL.Scheme, "http", "ws", 1)
		}
		targetURL.Path = targetURL.Path + c.Param("path")

		// Forward headers
		headers := http.Header{}
		for k, vv := range c.Request.Header {
			for _, v := range vv {
				headers.Add(k, v)
			}
		}

		upstreamConn, _, err := websocket.DefaultDialer.Dial(targetURL.String(), headers)
		if err != nil {
			logger.Error("Failed to dial upstream WS", zap.Error(err), zap.String("url", targetURL.String()))
			return
		}
		defer upstreamConn.Close()

		errc := make(chan error, 2)

		// Client to Upstream
		go func() {
			for {
				msgType, msg, err := clientConn.ReadMessage()
				if err != nil {
					errc <- err
					return
				}
				if err := upstreamConn.WriteMessage(msgType, msg); err != nil {
					errc <- err
					return
				}
			}
		}()

		// Upstream to Client
		go func() {
			for {
				msgType, msg, err := upstreamConn.ReadMessage()
				if err != nil {
					errc <- err
					return
				}
				if err := clientConn.WriteMessage(msgType, msg); err != nil {
					errc <- err
					return
				}
			}
		}()

		<-errc
	}
}
