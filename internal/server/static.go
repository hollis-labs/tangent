// Package server hosts the Tangent HTTP server and its embedded SPA.
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
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

	// Detect whether a real index.html is embedded. If not, serve the
	// placeholder for every path.
	if _, err := fs.ReadFile(sub, "index.html"); err != nil {
		return http.HandlerFunc(servePlaceholder)
	}

	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if _, err := fs.Stat(sub, reqPath); err != nil {
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
		}
		fileServer.ServeHTTP(w, r)
	})
}

func servePlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write([]byte(placeholderHTML)); err != nil {
		// Nothing useful to do — connection is already broken.
		return
	}
}
