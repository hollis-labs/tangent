package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// This file mounts the ADR 0007 §4 plugin-served browser routes
// (CW-20260910-0030).
//
// # What it buys
//
// A button in a room that does work without spending an agent turn. The
// alternative — a model round trip to re-issue a mechanical call because the UI
// had no way to make it — is waste that a user feels every time they press it.
//
// # A plugin route is not a side door
//
// Every route here goes through registerParticipantRoute, the same door
// /api/hitl, /api/rooms, /api/channels and /api/effects go through, so it
// carries the same-origin guard, the participant-session requirement and the
// ADR 0004 §7 capability check by construction rather than by remembering to.
// That is also what puts plugin routes inside
// TestEveryParticipantGuardedRouteUsesACapabilityParticipantsHold: they appear
// in ParticipantRoutes() like any other route, and a plugin cannot register one
// the check cannot see.
//
// # What does not cross the boundary
//
// **Inbound: the session cookie.** ADR 0004 §6.1 makes the session id
// capability material, and a plugin holding it could replay a participant's
// authority against any route on this server. The guard has already run by the
// time a plugin sees the request; what reaches the handler is the request
// *content*, never the credential that authorized it. Headers are allowlisted
// rather than filtered for the same reason a deny-list of secrets is always one
// header out of date.
//
// **Outbound: Set-Cookie.** A plugin that could set a cookie could mint or
// overwrite a participant session. Response headers are allowlisted too, and a
// dropped header is logged by name rather than discarded quietly — a plugin
// author reading the log learns why their header vanished, which is the whole
// difference between a boundary and a bug.

// maximumPluginRequestBytes caps a plugin route's request body. It matches the
// HITL command cap rather than the effect broker's larger one: a plugin route
// is a button, and a megabyte of button is a different feature that should
// arrive as a decision rather than as a default.
const maximumPluginRequestBytes = 32 * 1024

// pluginRequestHeaders are the request headers a plugin handler receives.
// Allowlisted, not filtered: Cookie is the participant's capability material
// (ADR 0004 §6.1) and Authorization would be the same for any future scheme, so
// the list names what a plugin legitimately needs to read a body and choose a
// representation, and nothing else.
var pluginRequestHeaders = []string{"Content-Type", "Accept", "Accept-Language"}

// pluginResponseHeaders are the response headers a plugin handler may set.
// Set-Cookie is the one that matters — a plugin that could set a cookie could
// mint a participant session — but the list is an allowlist for the same reason
// the request side is.
var pluginResponseHeaders = []string{"Content-Type", "Cache-Control", "Content-Language"}

// registerPluginRoutes mounts every plugin-contributed route.
//
// It refuses rather than skips. pluginhost has already checked each route at
// registration; this is the second check, at the layer that actually mounts
// them, because a route reaching a mux with a capability no participant holds
// would be a route that answers 403 forever and looks like a plugin bug.
func registerPluginRoutes(
	mux *http.ServeMux,
	routes *[]ParticipantRoute,
	cfg Config,
	logger *slog.Logger,
) error {
	if cfg.Participants == nil && len(cfg.PluginRoutes) > 0 {
		return fmt.Errorf(
			"server: %d plugin route(s) configured with no participant gate; the gate is what "+
				"authorizes them, and mounting them without one would publish them unguarded",
			len(cfg.PluginRoutes))
	}
	for _, route := range cfg.PluginRoutes {
		if !authz.Grants(authz.KindParticipant, route.Capability) {
			return fmt.Errorf(
				"server: plugin route %s is gated on %q, which ADR 0004 §7's participant row never "+
					"holds; no browser session could pass this guard",
				route.Pattern(), route.Capability)
		}
		if !strings.HasPrefix(route.Path, pluginhost.RoutePrefix) {
			return fmt.Errorf(
				"server: plugin route %s is outside the reserved %s prefix",
				route.Pattern(), pluginhost.RoutePrefix)
		}
		registerParticipantRoute(mux, routes, cfg, route.Pattern(), route.Capability,
			pluginRouteHandler(route, logger))
	}
	return nil
}

// pluginRouteHandler adapts one plugin's SDK dispatch interface to net/http.
//
// Streaming is not offered, per the SDK's own comment on HTTPHandler: the
// plugin returns a status, headers and a body, and this writes them. There is
// no flush and no SSE here, and adding one would be a contract change on the
// SDK's side first.
func pluginRouteHandler(route pluginhost.HTTPRoute, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maximumPluginRequestBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writePluginError(w, http.StatusRequestEntityTooLarge, "request_too_large",
					fmt.Sprintf("a plugin route accepts at most %d bytes", maximumPluginRequestBytes))
				return
			}
			writePluginError(w, http.StatusBadRequest, "unreadable_request", "could not read the request body")
			return
		}

		response, callErr := callPluginRoute(r, route, body)
		if callErr != nil {
			// The message is the plugin's, not a stack trace: this renders in
			// the SPA, and a participant who pressed a button deserves the
			// reason the plugin gave for refusing.
			logger.Error("plugin route failed",
				"method", route.Method, "path", route.Path, "error", callErr)
			writePluginError(w, http.StatusBadGateway, "plugin_error", callErr.Error())
			return
		}

		dropped := writePluginHeaders(w, response.Headers)
		if len(dropped) > 0 {
			logger.Warn("plugin route response headers dropped",
				"path", route.Path, "headers", strings.Join(dropped, ", "),
				"allowed", strings.Join(pluginResponseHeaders, ", "))
		}
		status := response.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if len(response.Body) > 0 {
			_, _ = w.Write(response.Body)
		}
	}
}

// callPluginRoute dispatches to the plugin, containing a panic.
//
// A plugin defect must not take down the server that every other room is
// running in, and a participant pressing a button must get an answer either
// way. The recovered value becomes an ordinary error, so the caller renders it
// through the same refusal path a returned error takes.
func callPluginRoute(
	r *http.Request,
	route pluginhost.HTTPRoute,
	body []byte,
) (response subprocess.HTTPResponse, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = subprocess.HTTPResponse{}
			err = fmt.Errorf("plugin route %s panicked: %v", route.Pattern(), recovered)
		}
	}()

	query := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			// One value per key: the SDK's HTTPRequest.Query is a flat map, so
			// a repeated parameter cannot be represented. Taking the first is
			// what net/http's own Query().Get does.
			query[key] = values[0]
		}
	}
	headers := map[string]string{}
	for _, name := range pluginRequestHeaders {
		if value := r.Header.Get(name); value != "" {
			headers[name] = value
		}
	}
	// SessionID is deliberately empty. It is the participant session's id, it
	// is capability material (ADR 0004 §6.1), and the gate that needed it has
	// already run.
	return route.Handler.HTTPHandle(r.Context(), subprocess.HTTPRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   query,
		Headers: headers,
		Body:    body,
	})
}

// writePluginHeaders copies the allowed response headers and returns the names
// of the ones it refused, sorted, so the caller can name them in a log.
func writePluginHeaders(w http.ResponseWriter, headers map[string]string) []string {
	allowed := map[string]bool{}
	for _, name := range pluginResponseHeaders {
		allowed[http.CanonicalHeaderKey(name)] = true
	}
	var dropped []string
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if !allowed[canonical] {
			dropped = append(dropped, canonical)
			continue
		}
		w.Header().Set(canonical, value)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	sort.Strings(dropped)
	return dropped
}

// writePluginError renders a refusal in the shape the SPA already reads from
// every other browser route: a code it can branch on and a message it can show.
func writePluginError(w http.ResponseWriter, status int, code, message string) {
	writeHITLJSON(w, status, map[string]any{"code": code, "message": message})
}
