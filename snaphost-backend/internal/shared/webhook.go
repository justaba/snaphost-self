package shared

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

func WebhookAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientSecret := c.GetHeader("X-Webhook-Secret")
		if subtle.ConstantTimeCompare([]byte(clientSecret), []byte(secret)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
