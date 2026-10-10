package server

import (
	"net/http"

	"github.com/hollis-labs/tangent/internal/authz"
)

// Management holds lifecycle intent only. Config and secrets are deliberately
// absent; browser controls can consume this surface without adding a new owner.
func registerPluginManagement(mux *http.ServeMux, routes *[]ParticipantRoute, cfg Config) {
	registerParticipantRoute(mux, routes, cfg, "GET /api/plugin-management", authz.View, func(w http.ResponseWriter, r *http.Request) {
		writeHITLJSON(w, http.StatusOK, cfg.PluginHost.Inventory(r.Context()))
	})
	for _, action := range []string{"enable", "disable", "reload"} {
		registerParticipantRoute(mux, routes, cfg, "POST /api/plugin-management/{pluginID}/"+action, authz.Resolve, func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("pluginID")
			var err error
			if action == "reload" {
				err = cfg.PluginHost.Reload(r.Context(), id)
			} else {
				err = cfg.PluginHost.SetEnabled(r.Context(), id, action == "enable")
			}
			if err != nil {
				writePluginError(w, http.StatusConflict, "PLUGIN_LIFECYCLE_REFUSED", err.Error())
				return
			}
			writeHITLJSON(w, http.StatusOK, map[string]any{"plugin_id": id, "action": action})
		})
	}
}
