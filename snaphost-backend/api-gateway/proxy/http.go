package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// HTTP returns a gin handler that reverse proxies requests to the target
func HTTP(target string, logger *zap.Logger) gin.HandlerFunc {
	parsedURL, err := url.Parse(target)
	if err != nil {
		logger.Fatal("Invalid proxy target URL", zap.String("target", target), zap.Error(err))
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = parsedURL.Scheme
			req.URL.Host = parsedURL.Host
			req.Host = parsedURL.Host

			// Preserve the original request path so the upstream service sees
			// the same API route shape as the gateway.
			req.URL.Path = parsedURL.Path + req.URL.Path
		},
		Transport: &http.Transport{
			DialContext:           (&http.Transport{}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Add("X-Proxied-By", "api-gateway")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("HTTP proxy error", zap.Error(err), zap.String("upstream", parsedURL.Host))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			// The status is already written; a failed body write means the
			// client is gone, and there is nothing left to report it to.
			_, _ = w.Write([]byte(`{"error": "bad_gateway", "upstream": "` + parsedURL.Host + `"}`))
		},
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
