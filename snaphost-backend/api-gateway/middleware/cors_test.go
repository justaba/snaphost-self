package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSHandlesConfiguredPreflightBeforeAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CORS_ALLOW_ORIGINS", "https://staging.kinocassa.ru")

	r := gin.New()
	r.Use(CORS())
	r.Use(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	r.GET("/api/v1/billing", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/billing", nil)
	req.Header.Set("Origin", "https://staging.kinocassa.ru")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://staging.kinocassa.ru" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}
