// Package panel serves the operator UI out of the binary.
//
// The panel used to be a separate deployment: its own repository, its own
// build, its own static host, and CORS plus an API base URL to connect the
// two. A self-hosted platform that ships as one binary cannot ask its operator
// to stand up a second one, so the built assets are compiled in.
//
// Everything here is embedded at build time from dist/, which Vite writes. The
// directory is committed with a .gitkeep so that a checkout compiles before
// anyone has run the frontend build; in that state the panel is absent and
// this package says so rather than serving a blank page.
package panel

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// dist holds the Vite output. The all: prefix matters — without it, embed
// skips files beginning with _ or ., and Vite emits neither reliably enough to
// bet on.
//
//go:embed all:dist
var dist embed.FS

// ErrNotBuilt reports that the binary carries no panel. It is a real state
// rather than a broken one: `go build` in a fresh checkout produces it, and
// the API is fully usable without a UI.
var ErrNotBuilt = errors.New("panel: dist is empty; run the frontend build")

// index is the SPA entry point. Any path the router owns must return it, so it
// is read once at startup rather than per request.
const indexFile = "index.html"

// Assets returns the embedded panel and whether one is present.
func Assets() (fs.FS, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, indexFile); err != nil {
		return nil, ErrNotBuilt
	}
	return sub, nil
}

// Middleware serves the panel from inside the middleware chain, and must be
// registered before authentication.
//
// This is not a NoRoute fallback, which would have been the obvious shape.
// Gin runs the global middleware for NoRoute as well, so a panel registered
// there is behind Auth and Casbin: the login page would answer 401 to the only
// people who need it. Running before Auth is the whole point, and a middleware
// is the only place that ordering can be expressed.
//
// Requests it does not own — anything under an API prefix, and anything that
// is not a GET or HEAD — fall through untouched to the rest of the chain.
func Middleware(assets fs.FS) gin.HandlerFunc {
	h := Handler(assets)
	return func(c *gin.Context) {
		if !serves(c.Request) {
			c.Next()
			return
		}
		h.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

// serves reports whether a request belongs to the panel rather than the API.
func serves(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return !isReserved(path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/")))
}

// Handler serves the panel: static files where they exist, index.html for
// everything else.
//
// The fallback is what makes client-side routing work — /deploys/<id> is a
// route the browser knows and the server does not, and answering 404 would
// break every reload and every shared link. It is deliberately narrow: only
// GET and HEAD, and never a path under an API prefix, because a mistyped API
// route answering 200 with HTML is far harder to diagnose than a 404.
func Handler(assets fs.FS) http.Handler {
	index, err := fs.ReadFile(assets, indexFile)
	if err != nil {
		panic("panel: assets without " + indexFile)
	}
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if isReserved(clean) {
			http.NotFound(w, r)
			return
		}

		if name := strings.TrimPrefix(clean, "/"); name != "" {
			if f, err := assets.Open(name); err == nil {
				info, statErr := f.Stat()
				_ = f.Close()
				if statErr == nil && !info.IsDir() {
					files.ServeHTTP(w, r)
					return
				}
			}
		}

		serveIndex(w, r, index)
	})
}

// reserved paths belong to the API, not the router. A request for one that
// reached this handler did not match a route, and the honest answer is 404.
var reserved = []string{"/api/", "/ws/", "/internal/", "/health", "/metrics"}

func isReserved(p string) bool {
	for _, prefix := range reserved {
		if p == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// serveIndex writes the SPA shell. It is explicitly uncacheable: the asset
// files Vite emits carry content hashes and may be cached forever, but the
// document that names them must not be, or an upgraded panel keeps loading the
// previous build's bundles until the browser gives up on its copy.
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, strings.NewReader(string(index)))
}
