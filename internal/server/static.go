// Package server hosts the Tangent HTTP server and its embedded SPA.
package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
)

// distFS embeds the production Vite build of the Tangent SPA. The build
// is configured (in ui/vite.config.ts) to write its output here, into
// internal/server/ui_dist, so the embed directive can resolve it
// relative to this Go package.
//
// The `all:` prefix is required so that files starting with `.` or `_`
// (e.g. `.gitkeep`, source maps) are included. The directory must exist
// at compile time; `make build` runs `make build-ui` first to populate
// it. A `.gitkeep` placeholder is committed so `go build ./...` works
// against a fresh checkout that has not yet run the frontend build.
//
//go:embed all:ui_dist
var distFS embed.FS

// spaFileSystem returns the embedded SPA filesystem rooted at ui_dist.
func spaFileSystem() (fs.FS, error) {
	return fs.Sub(distFS, "ui_dist")
}

// placeholderHTML is served when the embedded SPA does not contain an
// index.html — i.e. when only the `.gitkeep` placeholder is present.
// This keeps `go build` + `./tangent` viable on a fresh checkout that
// has not yet run `make build-ui`.
const placeholderHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Tangent</title>
  <style>
    body { margin: 0; background: #0a0a0a; color: #e5e5e5; font-family: ui-sans-serif, system-ui, -apple-system, sans-serif; }
    main { display: flex; align-items: center; justify-content: center; min-height: 100vh; }
    h1 { font-size: 1.25rem; font-weight: 500; letter-spacing: 0.02em; }
  </style>
</head>
<body>
  <main><h1>Tangent development placeholder (no embedded UI in this build &mdash; run ` + "`make build-ui`" + `)</h1></main>
</body>
</html>`

// spaHandler returns an http.Handler that serves the embedded SPA. When
// the embed contains only the .gitkeep placeholder (no index.html), all
// requests fall back to placeholderHTML so the binary is runnable
// without first building the frontend. Once a real Vite build has been
// embedded, requests for missing paths fall back to index.html so
// client-side routing works.
func spaHandler() http.Handler {
	sub, err := spaFileSystem()
	if err != nil {
		return http.HandlerFunc(servePlaceholder)
	}
	return spaHandlerFS(sub)
}

// spaHandlerFS is spaHandler over an explicit filesystem, so tests can serve a
// built SPA without embedding one.
func spaHandlerFS(sub fs.FS) http.Handler {
	// Detect whether a real index.html is embedded. If not, serve the
	// placeholder for every path.
	if _, err := fs.ReadFile(sub, "index.html"); err != nil {
		return http.HandlerFunc(servePlaceholder)
	}

	fileServer := http.FileServer(http.FS(sub))
	etags := &etagCache{sub: sub, byName: map[string]string{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if _, err := fs.Stat(sub, reqPath); err == nil {
			setStaticCacheHeaders(w, etags, reqPath)
		} else {
			// Path not in the embed. Distinguish two cases:
			//   1. Static-asset miss (e.g. /assets/foo.js) — return
			//      404 so cache busting and load-error handling work.
			//   2. Client-side route (no file extension, e.g. /r/abc)
			//      — rewrite to index.html so React Router can pick
			//      it up.
			if path.Ext(reqPath) != "" {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/"
			setStaticCacheHeaders(w, etags, "index.html")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// setStaticCacheHeaders marks the embedded SPA cacheable the way its layout
// allows. Vite writes content-hashed filenames under /assets/, so a name never
// changes meaning and may be cached forever. Everything else (index.html, which
// names the current hashes, and unhashed files such as the favicon) must be
// revalidated on each load. A strong ETag from the embedded bytes lets that
// revalidation answer 304 without resending the body. The embed carries no
// modification times, so there is no Last-Modified.
func setStaticCacheHeaders(w http.ResponseWriter, etags *etagCache, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if etag := etags.get(name); etag != "" {
		w.Header().Set("ETag", etag)
	}
}

// etagCache memoizes ETags for one handler's filesystem. It is per handler so
// two handlers over different filesystems never share an ETag.
type etagCache struct {
	sub    fs.FS
	mu     sync.Mutex
	byName map[string]string
}

// get hashes a file once; the embed cannot change at runtime.
func (c *etagCache) get(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if etag, ok := c.byName[name]; ok {
		return etag
	}
	data, err := fs.ReadFile(c.sub, name)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	c.byName[name] = etag
	return etag
}

func servePlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write([]byte(placeholderHTML)); err != nil {
		// Nothing useful to do — connection is already broken.
		return
	}
}
