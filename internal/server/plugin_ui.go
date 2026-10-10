package server

import (
	"net/http"

	"github.com/hollis-labs/tangent/internal/pluginui"
)

// pluginUISealedHandler is intentionally UNMOUNTED. Composition must first earn
// authenticated provision, durable retention, owner/frame joins and the public
// runtime integration. It serves only exact captured responses; no child POST
// or plugin-selected URL can provision this store.
func pluginUISealedHandler(store *pluginui.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		response, ok := store.Read(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", response.MediaType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if response.CSP != "" {
			w.Header().Set("Content-Security-Policy", response.CSP)
			w.Header().Set("Permissions-Policy", response.PermissionsPolicy)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Timing-Allow-Origin", "*")
		}
		// Response writes may fail after headers/partial delivery; never retry or
		// substitute another artifact under this URL.
		_, _ = w.Write(response.Body)
	}
}
