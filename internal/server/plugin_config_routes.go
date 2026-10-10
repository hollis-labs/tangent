package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

type pluginConfigRequest struct {
	Scope    pluginconfig.Scope   `json:"scope"`
	Revision string               `json:"revision"`
	Changes  pluginconfig.Changes `json:"changes"`
}

// registerPluginConfig is mounted by composition only when a genuine scoped
// store exists. Management auth stays in the host; no credentials cross a plugin.
func registerPluginConfig(mux *http.ServeMux, routes *[]ParticipantRoute, cfg Config, store *pluginconfig.Store) {
	registerParticipantRoute(mux, routes, cfg, "GET /api/plugin-management/config", authz.View, func(w http.ResponseWriter, r *http.Request) {
		ids, err := store.PluginIDs(r.Context())
		if err != nil {
			writePluginConfigError(w, err)
			return
		}
		groups := []pluginconfig.Group{}
		for _, id := range ids {
			group, e := store.Group(r.Context(), id)
			if e != nil {
				writePluginConfigError(w, e)
				return
			}
			if len(group.Fields) > 0 {
				groups = append(groups, group)
			}
		}
		writeHITLJSON(w, http.StatusOK, map[string]any{"groups": groups, "scopes": store.Scopes()})
	})
	registerParticipantRoute(mux, routes, cfg, "GET /api/plugin-management/{pluginID}/config", authz.View, func(w http.ResponseWriter, r *http.Request) {
		scope := pluginconfig.Scope{Kind: r.URL.Query().Get("scope_kind"), ID: r.URL.Query().Get("scope_id")}
		snapshot, err := store.Read(r.Context(), r.PathValue("pluginID"), scope)
		if err != nil {
			writePluginConfigError(w, err)
			return
		}
		writeHITLJSON(w, http.StatusOK, snapshot)
	})
	for _, action := range []string{"validate", "save", "reset", "apply"} {
		registerParticipantRoute(mux, routes, cfg, "POST /api/plugin-management/{pluginID}/config/"+action, authz.Resolve, func(w http.ResponseWriter, r *http.Request) {
			input, err := decodePluginConfigRequest(w, r)
			if err != nil {
				writePluginError(w, http.StatusBadRequest, "PLUGIN_CONFIG_INVALID", "Invalid settings request.")
				return
			}
			id := r.PathValue("pluginID")
			if action == "apply" {
				if len(input.Changes.Set) > 0 || len(input.Changes.Unset) > 0 {
					writePluginConfigError(w, pluginconfig.ErrRefused)
					return
				}
				current, e := store.Read(r.Context(), id, input.Scope)
				if e != nil {
					writePluginConfigError(w, e)
					return
				}
				if current.Revision != input.Revision {
					writePluginConfigError(w, pluginconfig.ErrConflict)
					return
				}
				if e = cfg.PluginHost.Reload(r.Context(), id); e != nil {
					writePluginError(w, http.StatusConflict, "PLUGIN_CONFIG_APPLY_REFUSED", "Plugin reload refused; saved configuration remains pending.")
					return
				}
				snapshot, e := store.Read(r.Context(), id, input.Scope)
				if e != nil {
					writePluginConfigError(w, e)
					return
				}
				writeHITLJSON(w, http.StatusOK, snapshot)
				return
			}
			if action == "validate" {
				validation, e := store.Validate(r.Context(), id, input.Scope, input.Revision, input.Changes)
				if e != nil {
					writePluginConfigError(w, e)
					return
				}
				writeHITLJSON(w, http.StatusOK, validation)
				return
			}
			if action == "reset" && len(input.Changes.Set) > 0 {
				writePluginConfigError(w, pluginconfig.ErrRefused)
				return
			}
			snapshot, validation, e := store.Save(r.Context(), id, input.Scope, input.Revision, input.Changes)
			if e != nil {
				writePluginConfigError(w, e)
				return
			}
			if !validation.Valid {
				writeHITLJSON(w, http.StatusUnprocessableEntity, validation)
				return
			}
			writeHITLJSON(w, http.StatusOK, snapshot)
		})
	}
}
func decodePluginConfigRequest(w http.ResponseWriter, r *http.Request) (pluginConfigRequest, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maximumPluginRequestBytes))
	if err != nil {
		return pluginConfigRequest{}, err
	}
	// Public strict codec rejects unknown/null/duplicate/nested malformed fields.
	var input pluginConfigRequest
	if err = sdkmanifest.DecodeExtension(raw, &input); err != nil {
		return pluginConfigRequest{}, err
	}
	// Preserve numeric lexemes for scalar validation instead of rounding via float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return pluginConfigRequest{}, err
	}
	return input, nil
}
func writePluginConfigError(w http.ResponseWriter, err error) {
	code := "PLUGIN_CONFIG_REFUSED"
	status := http.StatusConflict
	message := "Settings operation refused."
	if errors.Is(err, pluginconfig.ErrConflict) {
		code = "PLUGIN_CONFIG_CONFLICT"
		message = "Settings changed; refresh before saving."
	}
	if errors.Is(err, pluginconfig.ErrSecret) {
		code = "PLUGIN_CONFIG_KEYCHAIN_UNAVAILABLE"
		message = "Host keychain unavailable; no settings update was applied."
	}
	writePluginError(w, status, code, message)
}
