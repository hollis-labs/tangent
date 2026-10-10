package server

import (
	"net/http"

	"github.com/hollis-labs/tangent/internal/pluginui"
)

// pluginUIArtifactHandler serves only captured, host-admitted generation bytes.
// Mount it through the participant View guard, alongside the registry, not the
// plugin RPC route. No filesystem path or plugin-selected redirect is served.
func pluginUIArtifactHandler(catalog *pluginui.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := catalog.Artifact(r.PathValue("owner"), r.PathValue("generation"), r.PathValue("digest"), r.PathValue("artifact"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.PathValue("artifact") {
		case "bundle.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case "style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Every delivery must still consult the live owner. HTTP cache reuse is not
		// a substitute for the current-generation admission check.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}
