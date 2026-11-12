package server

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/webui"
)

// The three operability surfaces, mounted separately because they answer
// different questions and have different consequences (see internal/health).
//
//	GET /healthz                       liveness   — the host's; touches nothing
//	GET /api/health/ready              readiness  — database, migrations, registry,
//	                                                renderer host, delivery worker
//	GET /api/health/capability         capability — every interaction kind, bounded
//	GET /api/health/capability/{kind}  capability — one requested kind
//
// /healthz is the Substrate health module's and keeps its path. The
// managed-runtime health probe in ~/.cerberus/projects/tangent.cerberus.yaml
// points at it, and that probe feeds a supervisor's restart decision: pointing
// it at readiness would make a slow or degraded dependency cause a restart
// loop. Readiness is what an operator and `cerberus resource doctor` read, with
// tangent.view; liveness is what a supervisor reads, with nothing.

const (
	// maxHealthResponseBytes is a hard ceiling on any health response. The
	// reports are structurally bounded already — fixed check lists, truncated
	// detail strings, a capped per-kind listing — so reaching this means a
	// bound above it was wrong, and a truncated answer during an incident
	// beats an unbounded one.
	maxHealthResponseBytes = 64 * 1024
)

// registerHealthRoutes mounts the readiness and capability probes under
// /api/health. Liveness is the host's /healthz, which the Substrate health
// module serves; the host owns the /healthz prefix, so the richer probes moved
// under /api and need tangent.view.
//
// It mounts them They are registered
// unconditionally: a build that cannot report readiness must say so, because
// "the reporter was never wired" and "the dependency is broken" are different
// incidents and a probe that answers 404 to the first one hides it.
func registerHealthRoutes(rt *router, reporter *health.Reporter) {
	rt.handle("GET /api/health/ready", access.View, false, &healthHandler{reporter: reporter, probe: probeReadiness})
	rt.handle("GET /api/health/capability", access.View, false, &healthHandler{reporter: reporter, probe: probeCapabilitySummary})
	rt.handle("GET /api/health/capability/{kind}", access.View, false, &healthHandler{reporter: reporter, probe: probeCapabilityKind})
}

type healthProbe int

const (
	probeReadiness healthProbe = iota
	probeCapabilitySummary
	probeCapabilityKind
)

type healthHandler struct {
	reporter *health.Reporter
	probe    healthProbe
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.reporter == nil {
		writeHealthJSON(w, http.StatusServiceUnavailable, health.ReadinessReport{
			Status: health.SummaryUnavailable,
			Probe:  health.ProbeReadiness,
			Checks: []health.Check{{
				Name:   "reporter",
				Status: health.StatusFail,
				Detail: "this build serves HTTP without a health reporter installed",
				Action: "Readiness cannot be evaluated, so a 200 from /healthz proves only that " +
					"the process is up. Deploy a build that wires the health reporter before " +
					"relying on any probe but liveness.",
			}},
		})
		return
	}
	switch h.probe {
	case probeReadiness:
		report := h.reporter.Readiness(r.Context())
		status := http.StatusOK
		if !report.Ready() {
			status = http.StatusServiceUnavailable
		}
		writeHealthJSON(w, status, report)
	case probeCapabilitySummary:
		report := h.reporter.CapabilitySummary()
		status := http.StatusOK
		if report.Status == health.SummaryUnavailable {
			status = http.StatusServiceUnavailable
		}
		writeHealthJSON(w, status, report)
	case probeCapabilityKind:
		report := h.reporter.Capability(r.PathValue("kind"))
		writeHealthJSON(w, capabilityStatusCode(report), report)
	}
}

// capabilityStatusCode maps a per-kind report onto HTTP.
//
// 404 is reserved for a kind nothing in this build answers to — a caller
// typo. A kind that exists and cannot be served is 503: the request was
// well-formed and the host is the thing that is not ready for it.
func capabilityStatusCode(report health.CapabilityReport) int {
	switch {
	case report.Presence == health.PresenceUnknown:
		return http.StatusNotFound
	case report.Status == health.StatusFail:
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}

// writeHealthJSON encodes a report under the response ceiling.
//
// It encodes into a buffer first so a partially-written body can never reach a
// client: a health response that is truncated mid-object is unparseable
// exactly when someone is trying to parse it.
func writeHealthJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{"status":"unavailable","probe":"readiness",` +
			`"operator_action":"the health report could not be encoded; report this build as defective"}`)
		status = http.StatusInternalServerError
	}
	if len(body) > maxHealthResponseBytes {
		body = []byte(`{"status":"unavailable","probe":"readiness",` +
			`"operator_action":"the health report exceeded its response ceiling and was withheld; ` +
			`query one kind at a time at /api/health/capability/{kind}"}`)
		status = http.StatusServiceUnavailable
	}
	header := w.Header()
	header.Set("Content-Type", "application/json")
	// Never cached. A stale readiness answer during an incident is worse than
	// no answer, and an intermediary that caches a 503 keeps a recovered
	// process looking broken.
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// RendererHostProbe reports whether this build can actually serve the
// browser-side document that hosts a definition's renderer.
//
// It lives here because internal/server owns the embed directive, and it is
// returned as a closure so a report describes the process now rather than at
// construction. It reads the embedded filesystem only — no disk, no network,
// and in dev-proxy mode it deliberately does not dial the Vite server, because
// a readiness probe that makes an outbound request can be made to hang by the
// thing it is reporting on.
func RendererHostProbe() func() health.RendererHost {
	return func() health.RendererHost {
		sub, err := webui.Assets()
		if err != nil {
			return health.RendererHost{
				Mode:   "embedded",
				Detail: "the embedded renderer-host filesystem could not be opened",
			}
		}
		return inspectRendererHost(sub)
	}
}

// inspectRendererHost is the embed-independent half, so the degraded states
// can be exercised against a real filesystem rather than described by a stub.
// The two that matter are both real build outcomes: a fresh checkout that has
// never run `make build-ui` embeds only `.gitkeep`, and a build whose Vite
// output went missing leaves index.html with no assets beside it.
func inspectRendererHost(sub fs.FS) health.RendererHost {
	host := health.RendererHost{Mode: "embedded"}
	if _, err := fs.ReadFile(sub, "index.html"); err != nil {
		host.Detail = "no index.html is embedded; this binary serves the placeholder page " +
			"and can host no renderer"
		return host
	}
	host.Present = true
	host.Assets = countRendererAssets(sub)
	return host
}

// countRendererAssets counts the built asset files beside the host document.
// A host document with zero assets is the Vite build having gone missing while
// index.html survived — a state that renders a blank page rather than an error.
func countRendererAssets(sub fs.FS) int {
	entries, err := fs.ReadDir(sub, "assets")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			count++
		}
	}
	return count
}
