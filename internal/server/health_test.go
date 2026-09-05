package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/health"
)

// The HTTP half of CW-20260825-0066. What matters at this layer is the
// contract a probe consumer reads: which path answers which question, which
// status code it carries, and that liveness keeps answering when everything
// under it has failed.

const healthFixtureManifest = `manifest_version: "1.0.0"
publisher: tangent
kind: tangent.fixture
version: "1.0"
revision: 1
title: "Fixture"
description: "A synthetic definition."
package_id: tangent.fixture
package_version: "1.0.0"
ownership_class: host-package
request_schema: request.schema.json
response_kind: data
compatibility_response_schema: absent
renderer:
  id: tangent.renderer.fixture
  class: react-component
  entry: "components/envelopes/Fixture#Fixture"
  trust_class: core-trusted
  fallback:
    preserves_meaning: false
    degradation: none
compatible_host_versions: ">=0.12.0 <1.0.0"
compatible_protocol_versions: ">=1 <2"
compatibility_class: additive
required_capabilities: []
draft_custody: disabled
sensitivity_default: normal
inline_payload_limit_bytes: 262144
client_persistence_prohibited: false
trust:
  assurance: content-addressed-registry
telemetry:
  emits: []
  redact_fields: []
  opt_in: true
`

type healthTestRegistry struct {
	materialized []definition.Materialized
	registered   []string
}

func (r healthTestRegistry) MaterializedDefinitions() []definition.Materialized {
	return r.materialized
}
func (r healthTestRegistry) RegisteredKinds() []string { return r.registered }

func materializeForHealthTest(t *testing.T, source string, policy definition.HostPolicy) definition.Materialized {
	t.Helper()
	manifest, err := definition.Parse([]byte(source))
	if err != nil {
		t.Fatalf("parse fixture manifest: %v", err)
	}
	materialized, err := definition.Materialize(manifest, definition.Material{
		ManifestSource: []byte(source),
		RequestSchema:  []byte(`{"type":"object"}`),
		SourceLocator:  "embedded:fixture",
	}, policy)
	if err != nil {
		t.Fatalf("materialize fixture: %v", err)
	}
	return materialized
}

func healthTestPolicy() definition.HostPolicy {
	return definition.HostPolicy{HostVersion: "0.12.0", ProtocolVersion: "1.0.0"}
}

// probeMux mounts only the health routes, so a failure names the probe rather
// than something else in the route table.
func probeMux(reporter *health.Reporter) *http.ServeMux {
	mux := http.NewServeMux()
	registerHealthRoutes(mux, reporter)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode probe response %q: %v", recorder.Body.String(), err)
	}
	return body
}

// TestLivenessKeepsAnsweringWhenEverythingUnderItHasFailed is the separation
// stated as a test. A supervisor reads /healthz; if it started failing because
// the database went away, a degraded process would be restarted in a loop
// instead of being repaired.
func TestLivenessKeepsAnsweringWhenEverythingUnderItHasFailed(t *testing.T) {
	t.Parallel()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "liveness.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	mux := probeMux(health.NewReporter(health.WithDatabase(db)))

	recorder := get(t, mux, "/healthz")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200 while the process is responding", recorder.Code)
	}
	// The frozen body: the managed-runtime health check in
	// ~/.cerberus/projects/tangent.cerberus.yaml points at this path.
	if !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("liveness body = %s, want the frozen status token", recorder.Body.String())
	}
	if body := decode(t, recorder); body["probe"] != health.ProbeLiveness {
		t.Fatalf("liveness probe field = %v, want %q", body["probe"], health.ProbeLiveness)
	}

	// And the same process, asked the readiness question, says no.
	if code := get(t, mux, "/readyz").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, want 503 with the database closed", code)
	}
}

func TestReadinessAnswers200WhenEveryDependencyIsPresent(t *testing.T) {
	t.Parallel()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "ready.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	available := materializeForHealthTest(t, healthFixtureManifest, healthTestPolicy())

	mux := probeMux(health.NewReporter(
		health.WithDatabase(db),
		health.WithDefinitionRegistry(healthTestRegistry{
			materialized: []definition.Materialized{available},
			registered:   []string{"tangent.fixture"},
		}),
		health.WithRendererHost(func() health.RendererHost {
			return health.RendererHost{Mode: "embedded", Present: true, Assets: 3}
		}),
		health.WithDeliveryWorker(func() health.DeliveryWorker {
			return health.DeliveryWorker{Authorized: true}
		}),
	))

	recorder := get(t, mux, "/readyz")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /readyz = %d (%s), want 200", recorder.Code, recorder.Body.String())
	}
	if body := decode(t, recorder); body["status"] != string(health.SummaryOK) {
		t.Fatalf("readiness status = %v, want ok", body["status"])
	}
	// A cached readiness answer keeps a recovered process looking broken.
	if store := recorder.Header().Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

// TestCapabilityRoutesCarryDistinctStatusCodes: a typo and an unservable kind
// are different answers, and an operator reading a status code alone should be
// able to tell them apart.
func TestCapabilityRoutesCarryDistinctStatusCodes(t *testing.T) {
	t.Parallel()
	available := materializeForHealthTest(t, healthFixtureManifest, healthTestPolicy())
	quarantined := materializeForHealthTest(t, strings.ReplaceAll(healthFixtureManifest,
		"required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), healthTestPolicy())
	quarantined.Manifest.Kind = "tangent.quarantined"
	if quarantined.State != definition.StateQuarantined {
		t.Fatalf("fixture state = %q, want quarantined", quarantined.State)
	}

	mux := probeMux(health.NewReporter(health.WithDefinitionRegistry(healthTestRegistry{
		materialized: []definition.Materialized{available, quarantined},
		registered:   []string{"tangent.fixture", "tangent.quarantined", "tangent.legacy"},
	})))

	for path, want := range map[string]int{
		"/healthz/capability/tangent.fixture":     http.StatusOK,
		"/healthz/capability/tangent.legacy":      http.StatusOK,
		"/healthz/capability/tangent.quarantined": http.StatusServiceUnavailable,
		"/healthz/capability/tangent.nonsense":    http.StatusNotFound,
	} {
		recorder := get(t, mux, path)
		if recorder.Code != want {
			t.Errorf("GET %s = %d, want %d (%s)", path, recorder.Code, want, recorder.Body.String())
		}
		body := decode(t, recorder)
		if body["probe"] != health.ProbeCapability {
			t.Errorf("GET %s probe = %v, want %q", path, body["probe"], health.ProbeCapability)
		}
		if want != http.StatusOK && body["operator_action"] == "" {
			t.Errorf("GET %s answered %d with no operator action", path, want)
		}
	}

	// The summary answers degraded rather than failing: one kind is servable.
	summary := get(t, mux, "/healthz/capability")
	if summary.Code != http.StatusOK {
		t.Fatalf("GET /healthz/capability = %d, want 200 while one kind is servable", summary.Code)
	}
}

// TestProbesWithoutAReporterSaySoRatherThanNotFound: an unwired build must be
// visible. A 404 would read as "this version has no readiness probe", which is
// indistinguishable from a stale client asking the wrong path.
func TestProbesWithoutAReporterSaySoRatherThanNotFound(t *testing.T) {
	t.Parallel()
	mux := probeMux(nil)

	if code := get(t, mux, "/healthz").Code; code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200: liveness needs no reporter", code)
	}
	for _, path := range []string{"/readyz", "/healthz/capability", "/healthz/capability/tangent.fixture"} {
		recorder := get(t, mux, path)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "health reporter") {
			t.Errorf("GET %s body = %s, want it to name the missing reporter", path, recorder.Body.String())
		}
	}
}

// --- renderer host ----------------------------------------------------------

// TestInspectRendererHostOnAFreshCheckout is the real state of a binary built
// without `make build-ui`: the embed carries only the committed .gitkeep.
func TestInspectRendererHostOnAFreshCheckout(t *testing.T) {
	t.Parallel()
	host := inspectRendererHost(fstest.MapFS{".gitkeep": &fstest.MapFile{}})
	if host.Present {
		t.Fatal("a placeholder-only embed reported a renderer host present")
	}
	if host.Mode != "embedded" || host.Detail == "" {
		t.Fatalf("renderer host = %+v, want an embedded mode with a stated reason", host)
	}
}

// TestInspectRendererHostWithMissingAssets is the half-built state: index.html
// survived and the Vite bundle did not, which renders a blank page rather than
// an error.
func TestInspectRendererHostWithMissingAssets(t *testing.T) {
	t.Parallel()
	host := inspectRendererHost(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	})
	if !host.Present || host.Assets != 0 {
		t.Fatalf("renderer host = %+v, want present with zero assets", host)
	}

	report := health.NewReporter(
		health.WithRendererHost(func() health.RendererHost { return host }),
	).Readiness(context.Background())
	for _, check := range report.Checks {
		if check.Name != health.CheckRendererHost {
			continue
		}
		if check.Status != health.StatusWarn {
			t.Fatalf("renderer host check = %+v, want warn on a bundle-less host document", check)
		}
	}
}

func TestInspectRendererHostCountsRealAssets(t *testing.T) {
	t.Parallel()
	host := inspectRendererHost(fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<!doctype html>")},
		"assets/index-abc.js":   &fstest.MapFile{Data: []byte("//")},
		"assets/index-abc.css":  &fstest.MapFile{Data: []byte("/**/")},
		"assets/nested/keep.js": &fstest.MapFile{Data: []byte("//")},
	})
	if !host.Present || host.Assets != 2 {
		t.Fatalf("renderer host = %+v, want present with the two top-level assets counted", host)
	}
}

// TestRendererHostProbeDoesNotDialTheDevServer: in dev-proxy mode the renderer
// host is a separate process. A readiness probe that made an outbound request
// could be made to hang by the thing it reports on.
func TestRendererHostProbeDoesNotDialTheDevServer(t *testing.T) {
	t.Parallel()
	// A port nothing is listening on. If the probe dialed, this would block or
	// error rather than return promptly.
	host := RendererHostProbe("http://127.0.0.1:1")()
	if host.Mode != "dev-proxy" {
		t.Fatalf("renderer host mode = %q, want dev-proxy", host.Mode)
	}
	if host.Present {
		t.Error("dev-proxy mode claimed to serve a renderer host it does not own")
	}
}

// TestRendererHostProbeReadsTheRealEmbed keeps the probe honest against the
// actual build output. It skips rather than fails on a checkout that has not
// run `make build-ui`, because that is a real and supported state — and the
// skip message is what tells a reader which one they are in.
func TestRendererHostProbeReadsTheRealEmbed(t *testing.T) {
	t.Parallel()
	sub, err := spaFileSystem()
	if err != nil {
		t.Fatalf("open embedded renderer host: %v", err)
	}
	if _, err := fs.ReadFile(sub, "index.html"); err != nil {
		t.Skip("no SPA bundle is embedded in this build; run `make build-ui` to exercise this path")
	}
	host := RendererHostProbe("")()
	if !host.Present || host.Mode != "embedded" {
		t.Fatalf("renderer host = %+v, want an embedded, present host", host)
	}
	if host.Assets == 0 {
		t.Error("the embedded host document has no assets beside it; the page would render blank")
	}
}

// --- bounding ---------------------------------------------------------------

// TestHealthResponsesAreBounded holds the response ceiling, and holds it by
// replacing an oversized body rather than truncating one: a health document cut
// mid-object is unparseable exactly when someone is trying to parse it.
func TestHealthResponsesAreBounded(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	oversized := map[string]string{"payload": strings.Repeat("x", maxHealthResponseBytes+1)}
	writeHealthJSON(recorder, http.StatusOK, oversized)

	if recorder.Body.Len() > maxHealthResponseBytes {
		t.Fatalf("response body is %d bytes, want at most %d", recorder.Body.Len(), maxHealthResponseBytes)
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("oversized response = %d, want 503", recorder.Code)
	}
	if !json.Valid(recorder.Body.Bytes()) {
		t.Fatalf("withheld response is not valid JSON: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "/healthz/capability/{kind}") {
		t.Errorf("withheld response = %s, want it to name the narrower query", recorder.Body.String())
	}
}
