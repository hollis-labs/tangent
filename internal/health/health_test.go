package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/effect"
)

// Every degraded state in this file is constructed, not described. A closed
// database is a real *sql.DB that was closed; a schema behind expected is a
// real migration rolled back; a quarantined kind is a real manifest run
// through the real materializer with a host policy that refuses it. A test
// that stubs a dependency to return an error string proves that the reporter
// can format an error, which is not the thing worth proving.

// --- fixtures ---------------------------------------------------------------

const fixtureManifest = `manifest_version: "1.0.0"
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

// hostPolicy is the shipped posture: a real host version inside the fixture's
// range, and — as in the standalone binary — no grantable effect capability.
func hostPolicy() definition.HostPolicy {
	return definition.HostPolicy{
		HostVersion:     "0.12.0",
		ProtocolVersion: "1.0.0",
	}
}

func materialize(t *testing.T, source string, policy definition.HostPolicy) definition.Materialized {
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

func edited(t *testing.T, old, replacement string) string {
	t.Helper()
	if !strings.Contains(fixtureManifest, old) {
		t.Fatalf("fixture no longer contains %q", old)
	}
	return strings.ReplaceAll(fixtureManifest, old, replacement)
}

// registry is a real DefinitionRegistry over real materialization results.
type registry struct {
	materialized []definition.Materialized
	registered   []string
}

func (r registry) MaterializedDefinitions() []definition.Materialized { return r.materialized }
func (r registry) RegisteredKinds() []string                          { return r.registered }

func availableRegistry(t *testing.T) registry {
	t.Helper()
	item := materialize(t, fixtureManifest, hostPolicy())
	if item.State != definition.StateAvailable {
		t.Fatalf("fixture state = %q, want available", item.State)
	}
	return registry{
		materialized: []definition.Materialized{item},
		registered:   []string{"tangent.fixture", "tangent.legacy"},
	}
}

// migratedDB returns a real, fully-migrated SQLite database on disk. The path
// carries a distinctive token so the redaction test can prove no report ever
// discloses where durable state lives.
const dbPathToken = "tangent-health-secret-path"

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := tangentdb.Open(filepath.Join(dir, dbPathToken+".db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func healthyReporter(t *testing.T) *Reporter {
	t.Helper()
	return NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost {
			return RendererHost{Mode: "embedded", Present: true, Assets: 3}
		}),
		WithDeliveryWorker(func() DeliveryWorker {
			return DeliveryWorker{Authorized: true, Scope: "tangent:room-workflow-delivery"}
		}),
		WithRuntime(Runtime{ManagedResource: "tangent-dev"}),
	)
}

func checkNamed(t *testing.T, report ReadinessReport, name string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("readiness report has no %q check: %+v", name, report.Checks)
	return Check{}
}

// --- liveness ---------------------------------------------------------------

// TestLivenessTouchesNothing is the whole point of separating the probes: a
// process whose database has been closed under it is still *alive*, and a
// liveness probe that says otherwise turns a degraded dependency into a
// restart loop.
func TestLivenessTouchesNothing(t *testing.T) {
	t.Parallel()
	report := Live()
	if report.Status != "ok" || report.Probe != ProbeLiveness {
		t.Fatalf("liveness = %+v, want status ok probe liveness", report)
	}
	// The frozen body the managed-runtime health probe reads. Changing it is a
	// silent change to a supervisor's restart decision.
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal liveness: %v", err)
	}
	if !strings.Contains(string(encoded), `"status":"ok"`) {
		t.Fatalf("liveness body = %s, want it to keep the frozen status token", encoded)
	}
}

// --- readiness: healthy baseline -------------------------------------------

func TestReadinessPassesWithEveryDependencyPresent(t *testing.T) {
	t.Parallel()
	report := healthyReporter(t).Readiness(context.Background())
	if report.Status != SummaryOK {
		t.Fatalf("readiness = %q, want ok; checks: %+v", report.Status, report.Checks)
	}
	if !report.Ready() {
		t.Fatal("a fully healthy process reported not ready")
	}
	for _, check := range report.Checks {
		if check.Status != StatusPass {
			t.Errorf("check %q = %q (%s)", check.Name, check.Status, check.Detail)
		}
		if check.Action != "" {
			t.Errorf("check %q recommends an action while passing: %q", check.Name, check.Action)
		}
	}
	if len(report.Checks) != 5 {
		t.Fatalf("readiness reported %d checks, want the fixed five", len(report.Checks))
	}
}

// --- readiness: a genuinely closed database --------------------------------

func TestReadinessFailsOnAClosedDatabase(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	reporter := NewReporter(
		WithDatabase(db),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost {
			return RendererHost{Mode: "embedded", Present: true, Assets: 3}
		}),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: true} }),
		WithRuntime(Runtime{ManagedResource: "tangent-dev"}),
	)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	report := reporter.Readiness(context.Background())
	if report.Ready() {
		t.Fatal("readiness said ready with the database closed — the exact defect this task closed")
	}
	database := checkNamed(t, report, CheckDatabase)
	if database.Status != StatusFail {
		t.Fatalf("database check = %q, want fail", database.Status)
	}
	if database.Action == "" {
		t.Fatal("a failing database check recommended no operator action")
	}
	// The schema is not guessed at while the database is unreachable.
	migrations := checkNamed(t, report, CheckMigrations)
	if migrations.Status != StatusFail || !strings.Contains(migrations.Detail, "not evaluated") {
		t.Fatalf("migrations check = %+v, want an explicit not-evaluated failure", migrations)
	}
	// The rest of the report still answers: a process can be schema-broken and
	// renderer-broken at once, and a probe that stops at the first failure
	// hides the second one.
	if renderer := checkNamed(t, report, CheckRendererHost); renderer.Status != StatusPass {
		t.Errorf("renderer host check = %q, want pass; one failure must not mask the others", renderer.Status)
	}
}

// --- readiness: a schema genuinely behind ----------------------------------

func TestReadinessFailsWhenSchemaIsBehindExpected(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	if err := tangentdb.RollbackOne(db); err != nil {
		t.Fatalf("roll back one migration: %v", err)
	}
	reporter := NewReporter(WithDatabase(db), WithDefinitionRegistry(availableRegistry(t)))

	report := reporter.Readiness(context.Background())
	migrations := checkNamed(t, report, CheckMigrations)
	if migrations.Status != StatusFail {
		t.Fatalf("migrations check = %+v, want fail", migrations)
	}
	if !strings.Contains(migrations.Detail, "behind") {
		t.Errorf("migrations detail = %q, want it to say the schema is behind", migrations.Detail)
	}
	if !strings.Contains(migrations.Action, "--migrate-only") {
		t.Errorf("migrations action = %q, want the command that fixes it", migrations.Action)
	}
	if report.Ready() {
		t.Fatal("a process one migration behind its binary reported ready")
	}
}

func TestReadinessFailsOnANeverMigratedDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := tangentdb.Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })

	report := NewReporter(WithDatabase(db)).Readiness(context.Background())
	migrations := checkNamed(t, report, CheckMigrations)
	if migrations.Status != StatusFail || !strings.Contains(migrations.Detail, "never been migrated") {
		t.Fatalf("migrations check = %+v, want a never-migrated failure", migrations)
	}
	// The database itself is fine — the two facts stay separate.
	if database := checkNamed(t, report, CheckDatabase); database.Status != StatusPass {
		t.Fatalf("database check = %+v, want pass on a reachable but unmigrated file", database)
	}
}

// TestReadinessFailsOnADirtySchema covers the state golang-migrate leaves
// behind when a migration fails part-way: the tables match no migration, and
// running the migration again is the wrong instruction.
func TestReadinessFailsOnADirtySchema(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	if _, err := db.Exec(`UPDATE schema_migrations SET dirty = 1;`); err != nil {
		t.Fatalf("mark schema dirty: %v", err)
	}

	report := NewReporter(WithDatabase(db)).Readiness(context.Background())
	migrations := checkNamed(t, report, CheckMigrations)
	if migrations.Status != StatusFail || !strings.Contains(migrations.Detail, "dirty") {
		t.Fatalf("migrations check = %+v, want a dirty-schema failure", migrations)
	}
	if !strings.Contains(migrations.Action, "--rollback-one") {
		t.Errorf("dirty-schema action = %q, want the rollback instruction rather than a re-run",
			migrations.Action)
	}
}

// TestReadinessFailsWhenSchemaIsAhead is the upgrade-rollback case: an older
// binary against a newer schema. Migrating down is the wrong instruction and
// the check must not give it.
func TestReadinessFailsWhenSchemaIsAhead(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	expected, err := tangentdb.ExpectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET version = ?;`, expected+1); err != nil {
		t.Fatalf("advance schema version: %v", err)
	}

	report := NewReporter(WithDatabase(db)).Readiness(context.Background())
	migrations := checkNamed(t, report, CheckMigrations)
	if migrations.Status != StatusFail || !strings.Contains(migrations.Detail, "ahead") {
		t.Fatalf("migrations check = %+v, want an ahead-of-binary failure", migrations)
	}
	if strings.Contains(migrations.Action, "--rollback-one") {
		t.Errorf("ahead-of-binary action = %q, must not tell an operator to migrate down",
			migrations.Action)
	}
}

// --- readiness: registry, renderer host, delivery worker -------------------

// TestReadinessWarnsOnAQuarantinedKind builds the quarantine for real: the
// manifest requires a host-mediated capability, and standalone host policy
// grants none, which is the fail-closed path ADR 0003 §8 C7 specifies.
func TestReadinessWarnsOnAQuarantinedKind(t *testing.T) {
	t.Parallel()
	quarantined := materialize(t, edited(t, "required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy())
	if quarantined.State != definition.StateQuarantined {
		t.Fatalf("fixture state = %q, want quarantined", quarantined.State)
	}
	available := materialize(t, fixtureManifest, hostPolicy())

	report := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(registry{
			materialized: []definition.Materialized{available, quarantined},
		}),
		WithRendererHost(func() RendererHost {
			return RendererHost{Mode: "embedded", Present: true, Assets: 3}
		}),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: true} }),
	).Readiness(context.Background())

	check := checkNamed(t, report, CheckDefinitionRegistry)
	if check.Status != StatusWarn {
		t.Fatalf("registry check = %+v, want warn: one kind is unservable but the host still serves", check)
	}
	if report.Status != SummaryDegraded {
		t.Fatalf("readiness = %q, want degraded", report.Status)
	}
	// Degraded still serves. Paging on a warning is how warnings stop being read.
	if !report.Ready() {
		t.Fatal("a degraded-but-serving process reported not ready")
	}
}

func TestReadinessFailsWhenEveryKindIsUnservable(t *testing.T) {
	t.Parallel()
	quarantined := materialize(t, edited(t, "required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy())
	report := NewReporter(WithDefinitionRegistry(registry{
		materialized: []definition.Materialized{quarantined},
	})).Readiness(context.Background())

	if check := checkNamed(t, report, CheckDefinitionRegistry); check.Status != StatusFail {
		t.Fatalf("registry check = %+v, want fail when nothing is servable", check)
	}
	if report.Ready() {
		t.Fatal("a host that can serve no interaction kind reported ready")
	}
}

func TestReadinessFailsOnAMissingRendererHost(t *testing.T) {
	t.Parallel()
	reporter := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		// The state a binary built without `make build-ui` is actually in:
		// the embed carries only .gitkeep, so every room loads a placeholder.
		WithRendererHost(func() RendererHost {
			return RendererHost{
				Mode:   "embedded",
				Detail: "no index.html is embedded; this binary serves the placeholder page",
			}
		}),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: true} }),
	)
	report := reporter.Readiness(context.Background())
	check := checkNamed(t, report, CheckRendererHost)
	if check.Status != StatusFail {
		t.Fatalf("renderer host check = %+v, want fail", check)
	}
	if !strings.Contains(check.Action, "make build") {
		t.Errorf("renderer host action = %q, want the rebuild instruction", check.Action)
	}
	if report.Ready() {
		t.Fatal("a host that can render nothing reported ready")
	}
}

func TestReadinessWarnsInDevProxyMode(t *testing.T) {
	t.Parallel()
	report := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost {
			return RendererHost{Mode: "dev-proxy", Detail: "reverse-proxied"}
		}),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: true} }),
	).Readiness(context.Background())

	check := checkNamed(t, report, CheckRendererHost)
	if check.Status != StatusWarn {
		t.Fatalf("dev-proxy renderer host = %+v, want warn: the probe did not observe the dev server", check)
	}
	if report.Status != SummaryDegraded {
		t.Fatalf("readiness = %q, want degraded in dev-proxy mode", report.Status)
	}
}

// TestReadinessFailsWhenDeliveryWorkerIsUnauthorized covers the failure that
// looks healthiest from outside: rooms open, participants answer, and no
// caller ever receives an outcome.
func TestReadinessFailsWhenDeliveryWorkerIsUnauthorized(t *testing.T) {
	t.Parallel()
	report := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost {
			return RendererHost{Mode: "embedded", Present: true, Assets: 3}
		}),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: false} }),
	).Readiness(context.Background())

	check := checkNamed(t, report, CheckDeliveryWorker)
	if check.Status != StatusFail {
		t.Fatalf("delivery worker check = %+v, want fail", check)
	}
	if strings.Contains(strings.ToLower(check.Action), "restart") &&
		!strings.Contains(check.Action, "do not restart") {
		t.Errorf("delivery worker action = %q, must not send an operator into a restart loop for "+
			"a build misconfiguration", check.Action)
	}
	if report.Ready() {
		t.Fatal("a host that can never deliver an outcome reported ready")
	}
}

// TestReadinessReportsAnUnwiredDependencyDistinctly: "the reporter was never
// wired" and "the dependency is broken" are different incidents.
func TestReadinessReportsAnUnwiredDependencyDistinctly(t *testing.T) {
	t.Parallel()
	report := NewReporter().Readiness(context.Background())
	if report.Ready() {
		t.Fatal("a reporter with no dependencies at all reported ready")
	}
	for _, name := range []string{
		CheckDatabase, CheckMigrations, CheckDefinitionRegistry,
		CheckRendererHost, CheckDeliveryWorker,
	} {
		check := checkNamed(t, report, name)
		if check.Status != StatusFail {
			t.Errorf("check %q = %q, want fail when nothing is installed", name, check.Status)
		}
		if check.Action == "" {
			t.Errorf("check %q failed with no operator action", name)
		}
	}
}

// --- capability health ------------------------------------------------------

func TestCapabilityReportsAvailableKind(t *testing.T) {
	t.Parallel()
	report := NewReporter(WithDefinitionRegistry(availableRegistry(t))).Capability("tangent.fixture")
	if report.Presence != PresenceManaged || !report.Usable || report.Status != StatusPass {
		t.Fatalf("capability report = %+v, want a usable managed kind", report)
	}
	if report.State != string(definition.StateAvailable) {
		t.Fatalf("state = %q, want the definition vocabulary's `available`", report.State)
	}
	if report.ManifestDigest == "" {
		t.Error("capability report carries no manifest digest to compare against generated types")
	}
}

// TestCapabilityDistinguishesTheFourUnusableStates is criterion 3's substance:
// the report reuses internal/definition's state vocabulary rather than
// collapsing four different fixes into one "unhealthy".
func TestCapabilityDistinguishesTheFourUnusableStates(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		materialized definition.Materialized
		want         definition.State
	}{
		"quarantined by an ungranted capability": {
			materialized: materialize(t, edited(t, "required_capabilities: []",
				"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy()),
			want: definition.StateQuarantined,
		},
		"incompatible with this host version": {
			materialized: materialize(t, fixtureManifest, definition.HostPolicy{
				HostVersion: "2.0.0", ProtocolVersion: "1.0.0",
			}),
			want: definition.StateIncompatible,
		},
		"unavailable because host policy disabled it": {
			materialized: func() definition.Materialized {
				policy := hostPolicy()
				policy.DisabledKinds = map[string]bool{"tangent.fixture": true}
				return materialize(t, fixtureManifest, policy)
			}(),
			want: definition.StateUnavailable,
		},
		"quarantined by the renderer trust ceiling": {
			// A sandboxed renderer asking for a capability its class refuses.
			// Widening host policy would not lift this one, and the report has
			// to say so or an operator grants and re-grants for an afternoon.
			materialized: materialize(t, strings.NewReplacer(
				"class: react-component", "class: sandboxed-frame",
				"trust_class: core-trusted", "trust_class: sandboxed-code",
				"required_capabilities: []",
				"required_capabilities:\n  - id: process.exec\n    optional: false",
			).Replace(fixtureManifest), hostPolicy()),
			want: definition.StateQuarantined,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.materialized.State != tc.want {
				t.Fatalf("fixture materialized to %q, want %q (%s)",
					tc.materialized.State, tc.want, tc.materialized.StateReason)
			}
			report := NewReporter(WithDefinitionRegistry(registry{
				materialized: []definition.Materialized{tc.materialized},
			})).Capability("tangent.fixture")

			if report.Usable {
				t.Fatalf("an %s kind reported usable", tc.want)
			}
			if report.State != string(tc.want) {
				t.Fatalf("state = %q, want %q", report.State, tc.want)
			}
			if report.StateReason == "" {
				t.Error("an unusable kind reported no reason")
			}
			if report.Action == "" {
				t.Error("an unusable kind recommended no operator action")
			}
			if report.ErrorCode == "" {
				t.Error("an unusable kind carries no upstream error code")
			}
		})
	}
}

func TestCapabilitySeparatesUnknownFromUnmanaged(t *testing.T) {
	t.Parallel()
	reporter := NewReporter(WithDefinitionRegistry(availableRegistry(t)))

	unmanaged := reporter.Capability("tangent.legacy")
	if unmanaged.Presence != PresenceUnmanaged {
		t.Fatalf("registered-but-unmanaged kind = %q, want unmanaged", unmanaged.Presence)
	}
	if !unmanaged.Usable {
		t.Error("a kind on the legacy registration path is served; reporting it unusable is wrong")
	}
	if unmanaged.Status != StatusWarn {
		t.Errorf("unmanaged status = %q, want warn: this report vouches for nothing", unmanaged.Status)
	}

	unknown := reporter.Capability("tangent.not-a-kind")
	if unknown.Presence != PresenceUnknown || unknown.Status != StatusFail {
		t.Fatalf("unknown kind = %+v, want an unknown failure", unknown)
	}
}

// TestCapabilityReportsUndeclaredEffectsTruthfully: no shipped manifest
// declares a host-mediated capability, so every effect request is refused
// effect_capability_undeclared. Reporting that as silence would let a reader
// infer that effects work.
func TestCapabilityReportsUndeclaredEffectsTruthfully(t *testing.T) {
	t.Parallel()
	report := NewReporter(WithDefinitionRegistry(availableRegistry(t))).Capability("tangent.fixture")
	if len(report.Effects.Declared) != 0 {
		t.Fatalf("fixture declared %d capabilities, want none", len(report.Effects.Declared))
	}
	if report.Effects.RequestOutcome != effect.CodeCapabilityUndeclared {
		t.Fatalf("effect request outcome = %q, want %q",
			report.Effects.RequestOutcome, effect.CodeCapabilityUndeclared)
	}
	if report.Effects.Note == "" {
		t.Error("an undeclared effect posture carries no explanation")
	}

	summary := NewReporter(WithDefinitionRegistry(availableRegistry(t))).CapabilitySummary()
	if len(summary.DeclaredEffectCapabilities) != 0 {
		t.Fatalf("summary declared %v, want none in this build", summary.DeclaredEffectCapabilities)
	}
	if !strings.Contains(summary.EffectPostureNote, effect.CodeCapabilityUndeclared) {
		t.Errorf("summary note = %q, want it to name the refusal code", summary.EffectPostureNote)
	}
}

// TestCapabilitySeparatesTrustDenialFromPolicyDenial is why the two refusals
// are not one field: widening host policy lifts exactly one of them.
func TestCapabilitySeparatesTrustDenialFromPolicyDenial(t *testing.T) {
	t.Parallel()
	// Optional so the definition still materializes and the denial is visible
	// on an otherwise-available kind.
	source := strings.NewReplacer(
		"class: react-component", "class: sandboxed-frame",
		"trust_class: core-trusted", "trust_class: sandboxed-code",
		"required_capabilities: []",
		"required_capabilities:\n  - id: process.exec\n    optional: true",
	).Replace(fixtureManifest)
	item := materialize(t, source, hostPolicy())

	report := NewReporter(WithDefinitionRegistry(registry{
		materialized: []definition.Materialized{item},
	})).Capability("tangent.fixture")

	if len(report.Effects.Declared) != 1 {
		t.Fatalf("declared capabilities = %+v, want one", report.Effects.Declared)
	}
	declared := report.Effects.Declared[0]
	if declared.Grant != GrantDeniedByTrustClass {
		t.Fatalf("grant = %q, want %q: the trust ceiling refused it before host policy was asked",
			declared.Grant, GrantDeniedByTrustClass)
	}
	if declared.Mediation == "" {
		t.Error("a declared capability reports no mediation, so a reader cannot tell a barrier from a record")
	}
}

func TestCapabilitySummaryCountsStatesAndBoundsItsListing(t *testing.T) {
	t.Parallel()
	quarantined := materialize(t, edited(t, "required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy())
	available := materialize(t, fixtureManifest, hostPolicy())

	summary := NewReporter(WithDefinitionRegistry(registry{
		materialized: []definition.Materialized{available, quarantined},
		registered:   []string{"tangent.fixture", "tangent.legacy"},
	})).CapabilitySummary()

	if summary.ManagedDefinitions != 2 || summary.Usable != 1 {
		t.Fatalf("summary counts = %+v, want two managed and one usable", summary)
	}
	if summary.RegisteredKinds != 2 {
		t.Fatalf("registered kinds = %d, want 2", summary.RegisteredKinds)
	}
	if summary.StateCounts[string(definition.StateQuarantined)] != 1 {
		t.Fatalf("state counts = %v, want one quarantined", summary.StateCounts)
	}
	if summary.Status != SummaryDegraded || summary.Action == "" {
		t.Fatalf("summary = %q/%q, want degraded with an action", summary.Status, summary.Action)
	}
	if len(summary.Unusable) != 1 || summary.Truncated {
		t.Fatalf("unusable listing = %d entries truncated=%v, want the one unservable kind",
			len(summary.Unusable), summary.Truncated)
	}
}

func TestCapabilitySummaryTruncatesAtItsCeiling(t *testing.T) {
	t.Parallel()
	quarantined := materialize(t, edited(t, "required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy())
	many := make([]definition.Materialized, 0, maxListedKinds+5)
	for i := 0; i < maxListedKinds+5; i++ {
		many = append(many, quarantined)
	}
	summary := NewReporter(WithDefinitionRegistry(registry{materialized: many})).CapabilitySummary()
	if len(summary.Unusable) != maxListedKinds || !summary.Truncated {
		t.Fatalf("listing = %d entries truncated=%v, want the ceiling honored and declared",
			len(summary.Unusable), summary.Truncated)
	}
}

// --- bounding and redaction -------------------------------------------------

// TestReportsNeverDiscloseWhereDurableStateLives is criterion 4's leak test.
// The database path carries a distinctive token; no report may echo it, no
// matter how badly the database is behaving.
func TestReportsNeverDiscloseWhereDurableStateLives(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	reporter := NewReporter(
		WithDatabase(db),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost { return RendererHost{Mode: "embedded"} }),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: false} }),
		WithRuntime(Runtime{ManagedResource: "tangent-dev"}),
	)
	// Break it as thoroughly as possible: the driver errors that follow are
	// exactly the ones that carry a file path.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	documents := [][]byte{
		mustMarshal(t, reporter.Readiness(context.Background())),
		mustMarshal(t, reporter.CapabilitySummary()),
		mustMarshal(t, reporter.Capability("tangent.fixture")),
		mustMarshal(t, Live()),
	}
	for _, document := range documents {
		if strings.Contains(string(document), dbPathToken) {
			t.Fatalf("a health report disclosed the database path: %s", document)
		}
		for _, forbidden := range []string{".db", "/var/", "/tmp/", "sqlite3"} {
			if strings.Contains(string(document), forbidden) {
				t.Errorf("a health report contains %q, which is filesystem detail: %s",
					forbidden, document)
			}
		}
	}
}

// TestReportsStayBounded holds the ceilings that make a report readable during
// an incident.
func TestReportsStayBounded(t *testing.T) {
	t.Parallel()
	overlong := strings.Repeat("x", maxDetailBytes*4)
	if bounded := bound(overlong); len(bounded) > maxDetailBytes+len("…") {
		t.Fatalf("bound produced %d bytes, want at most %d", len(bounded), maxDetailBytes+len("…"))
	}
	// Truncation must not produce invalid UTF-8: a report is JSON, and invalid
	// UTF-8 in a string is a document nobody can parse.
	multibyte := strings.Repeat("é", maxDetailBytes)
	if bounded := bound(multibyte); !json.Valid(mustMarshal(t, bounded)) {
		t.Fatalf("bounded multibyte string is not valid JSON: %q", bounded)
	}

	reporter := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithRendererHost(func() RendererHost { return RendererHost{Mode: "embedded"} }),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: false} }),
	)
	report := reporter.Readiness(context.Background())
	for _, check := range report.Checks {
		if len(check.Detail) > maxDetailBytes+len("…") {
			t.Errorf("check %q detail is %d bytes", check.Name, len(check.Detail))
		}
		if len(check.Action) > 512 {
			t.Errorf("check %q action is %d bytes; an unreadable instruction is not an instruction",
				check.Name, len(check.Action))
		}
	}
}

// TestEveryNonPassingCheckRecommendsAnAction is criterion 4 stated directly:
// "database: error" is a symptom, not an operator action.
func TestEveryNonPassingCheckRecommendsAnAction(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	reporters := []*Reporter{
		NewReporter(),
		NewReporter(WithDatabase(db)),
		NewReporter(WithDefinitionRegistry(registry{})),
	}
	for _, reporter := range reporters {
		for _, check := range reporter.Readiness(context.Background()).Checks {
			if check.Status == StatusPass {
				continue
			}
			if check.Action == "" {
				t.Errorf("check %q is %q with no operator action", check.Name, check.Status)
			}
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return encoded
}
