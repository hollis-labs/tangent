package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hollis-labs/plugin-sdk/registry"
)

// registerPluginRegistryRoute mounts GET /api/plugins/registry.
//
// This is the minimal proof endpoint for CW-20260911-0035 — it serves a
// registry.Response so the browser loader from @hollis-labs/plugin-registry
// can resolve plugins. Currently returns an empty registry (no plugins
// registered) as the validation target; full plugin discovery and contribution
// collection is follow-on adoption work.
func registerPluginRegistryRoute(
	mux *http.ServeMux,
	logger *slog.Logger,
) {
	mux.HandleFunc("GET /api/plugins/registry", func(w http.ResponseWriter, r *http.Request) {
		resp := buildRegistryResponse()

		payload, err := json.Marshal(resp)
		if err != nil {
			logger.Error("plugin registry serialization failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
}

// buildRegistryResponse constructs a registry.Response from the plugin host state.
//
// Minimal proof implementation: returns an empty registry with protocol=1.
// Future work will populate from host.Plugins() and collect contributions from
// registered MCP tools, HTTP routes, and envelope kinds.
func buildRegistryResponse() registry.Response {
	resp := registry.NewResponse()

	// Minimal proof: empty registry demonstrates the wire contract works.
	// Full implementation would iterate host.Plugins() and collect:
	//   - Plugin entries with bundle URLs from plugin manifests
	//   - Contributions from MCP tools, HTTP routes, envelope kinds
	//   - Meta fields carrying host-specific taxonomy (priority, labels, etc)

	return resp
}
