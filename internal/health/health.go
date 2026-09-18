// Package health separates the three operability questions that a single
// `/healthz` collapsed into one answer.
//
// The three are not degrees of the same check. They answer different
// questions, are read by different consumers, and have different consequences
// when they fail:
//
//   - Liveness: is this process responding? Nothing more. It deliberately
//     touches no dependency, because a supervisor restarts on a liveness
//     failure and a probe that fails when a query is slow converts a slow
//     query into a restart loop.
//   - Readiness: can this process serve traffic right now? Database,
//     migrations at the version this binary expects, the definition registry
//     materialized, the renderer host present, the delivery worker able to
//     hand terminal outcomes back.
//   - Capability health: can it serve *this particular interaction kind*?
//     Per-kind, and reported in the materialization-state vocabulary
//     internal/definition already owns rather than a second one invented here.
//
// A `/healthz` that returns 200 while the database is gone is worse than no
// probe at all: it converts a loud failure into a silent one. That is the
// defect this package exists to close.
//
// # What a report may contain
//
// Every response here is read during an incident, which makes it the most
// likely thing in the process to be pasted into a chat window. So it carries
// no payload, no participant text, no draft, no evidence, no filesystem path,
// no session id, and no capability material. It carries host-published facts —
// kinds, versions, digests, materialization states, capability ids, counts —
// and, for anything that is not passing, one sentence saying what the operator
// should do. "database: error" is a symptom; an operator action is the point.
package health

import (
	"context"
	"database/sql"
	"time"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Probe names. They appear in every response so a reader holding one JSON
// document knows which question it answers.
const (
	ProbeLiveness   = "liveness"
	ProbeReadiness  = "readiness"
	ProbeCapability = "capability"
)

// Status is one dependency's verdict. It matches the pass/warn/fail vocabulary
// `cerberus resource doctor` already prints, so an operator reading both is
// reading one vocabulary.
type Status string

const (
	StatusPass Status = "pass"
	// StatusWarn means the process is serving but something needs attention
	// before it stops being true.
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Summary is a whole report's verdict.
type Summary string

const (
	// SummaryOK means every check passed.
	SummaryOK Summary = "ok"
	// SummaryDegraded means the process can serve traffic but at least one
	// dependency warned. It is deliberately not a failure: an operator who is
	// paged for every warning stops reading them.
	SummaryDegraded Summary = "degraded"
	// SummaryUnavailable means at least one check failed and the process
	// cannot be trusted to serve.
	SummaryUnavailable Summary = "unavailable"
)

// Check names. Fixed, so a report's shape is the same every time it is read
// and a difference between two reports is a difference in state.
const (
	CheckDatabase           = "database"
	CheckMigrations         = "migrations"
	CheckDefinitionRegistry = "definition_registry"
	CheckRendererHost       = "renderer_host"
	CheckDeliveryWorker     = "delivery_worker"
)

const (
	// probeBudget bounds how long a readiness probe may take. A probe that can
	// hang is a probe that turns one stuck dependency into a stuck operator.
	probeBudget = 2 * time.Second

	// maxDetailBytes bounds one detail or action string. Reports are read in
	// terminals during incidents; an unbounded reason field is how a probe
	// becomes unreadable exactly when it matters.
	maxDetailBytes = 240

	// maxListedKinds bounds how many per-kind entries a summary carries. The
	// registry is small today (eighteen managed definitions), but a bound that
	// only holds because the input is small is not a bound.
	maxListedKinds = 50
)

// Check is one dependency's answer.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Detail says what was observed. It never names a path, a payload, or a
	// principal.
	Detail string `json:"detail,omitempty"`
	// Action says what the operator should do. Empty only when Status is
	// StatusPass, because there is nothing to do.
	Action string `json:"operator_action,omitempty"`
	// Correlation names where this check's history lives. It is populated only
	// for a check that is not passing: a passing check has no incident to
	// correlate, and putting a trace id on every line would train a reader to
	// skip them.
	Correlation *Correlation `json:"correlation,omitempty"`
}

// LivenessReport is the whole of liveness.
//
// `status: ok` is a frozen token, not a Summary: the managed-runtime health
// probe in ~/.cerberus/projects/tangent.cerberus.yaml reads this endpoint, and
// changing the body it has always returned would be a silent change to a
// supervisor's restart decision.
type LivenessReport struct {
	Status string `json:"status"`
	Probe  string `json:"probe"`
}

// Live builds the liveness answer. It takes no context and no dependency
// because the ability to return it *is* the thing being proved.
func Live() LivenessReport {
	return LivenessReport{Status: "ok", Probe: ProbeLiveness}
}

// ReadinessReport is the whole of readiness.
type ReadinessReport struct {
	Status Summary `json:"status"`
	Probe  string  `json:"probe"`
	Checks []Check `json:"checks"`
}

// Ready reports whether the process may serve traffic. Degraded counts as
// serving; only a failed check does not.
func (r ReadinessReport) Ready() bool { return r.Status != SummaryUnavailable }

// RendererHost describes the browser-side document that loads a definition's
// renderer. It is a build fact, not a dependency to dial.
type RendererHost struct {
	// Mode is `embedded` when the binary carries the SPA bundle, and
	// `dev-proxy` when non-API requests are reverse-proxied to a Vite dev
	// server.
	Mode string `json:"mode"`
	// Present is whether a renderer host document can actually be served.
	Present bool `json:"present"`
	// Assets counts the built asset files. Zero with Present true is the
	// placeholder page, which hosts no renderer.
	Assets int `json:"assets"`
	// Detail carries the build fact behind Present. It names no path.
	Detail string `json:"detail,omitempty"`
}

// DeliveryWorker describes the in-process caller-pull adapter that hands a
// terminal outcome back over the caller's own MCP request.
//
// It is a policy, not a goroutine: nothing here polls. What readiness asks is
// whether the installed DeliveryWorkerPolicy admits the adapter, because a
// build whose policy denies it accepts participant resolutions and then never
// delivers them — the failure mode that looks healthiest from outside.
type DeliveryWorker struct {
	// Authorized is whether the installed policy admits the in-process
	// caller-pull adapter's actor binding.
	Authorized bool `json:"authorized"`
	// Scope is the adapter's actor scope. It is a host constant, not a
	// participant's or a caller's identity.
	Scope string `json:"scope,omitempty"`
}

// DefinitionRegistry is the materialized interaction-definition registry.
// *envelope.Service satisfies it; the interface is declared here so this
// package depends on the definition vocabulary and not on the envelope
// service's lifecycle.
type DefinitionRegistry interface {
	MaterializedDefinitions() []definition.Materialized
	RegisteredKinds() []string
}

// Runtime is what this process knows about its own supervision. It exists so
// operator actions can name a command rather than describe a wish, without
// this package hard-coding one installation's resource id.
type Runtime struct {
	// ManagedResource is the Cerberus resource id that owns this process's
	// lifecycle, when one does. Empty is normal: an unmanaged `./tangent` has
	// no resource to name, and the actions phrase themselves generically.
	ManagedResource string
}

// Reporter answers all three questions from one set of dependencies.
//
// Every dependency is optional in the Go sense — a nil handle or probe is a
// state the reporter has to be able to describe, because "the thing that
// reports on X was never wired" and "X is broken" are different incidents and
// a probe that conflates them is the same defect this package exists to close.
type Reporter struct {
	db       *sql.DB
	registry DefinitionRegistry
	renderer func() RendererHost
	delivery func() DeliveryWorker
	// plugins reports which plugins the host loaded and which it refused. Nil
	// is a valid state and reads as a warning rather than a failure: a build
	// with no probe is not a build with broken plugins. See plugins.go.
	plugins func(ctx context.Context) PluginInventory
	runtime Runtime

	// telemetry records readiness transitions and links failures to the trace
	// their history is filed under. Nil is a valid state: a build with no
	// telemetry still reports, it just cannot say what happened before now.
	telemetry   *telemetry.Recorder
	transitions checkTransitions
}

// Option configures a Reporter.
type Option func(*Reporter)

// WithDatabase installs the shared durable-state handle.
func WithDatabase(db *sql.DB) Option {
	return func(r *Reporter) { r.db = db }
}

// WithDefinitionRegistry installs the materialized registry.
func WithDefinitionRegistry(registry DefinitionRegistry) Option {
	return func(r *Reporter) { r.registry = registry }
}

// WithRendererHost installs the renderer-host build probe. It is a function
// rather than a value so a report describes the process now, not at boot.
func WithRendererHost(probe func() RendererHost) Option {
	return func(r *Reporter) { r.renderer = probe }
}

// WithDeliveryWorker installs the delivery-worker authorization probe.
func WithDeliveryWorker(probe func() DeliveryWorker) Option {
	return func(r *Reporter) { r.delivery = probe }
}

// WithRuntime records how this process is supervised.
func WithRuntime(runtime Runtime) Option {
	return func(r *Reporter) { r.runtime = runtime }
}

// NewReporter builds a reporter. It never fails: a reporter missing a
// dependency reports that it is missing, which is more useful during a boot
// failure than refusing to exist.
func NewReporter(options ...Option) *Reporter {
	reporter := &Reporter{}
	for _, option := range options {
		if option != nil {
			option(reporter)
		}
	}
	return reporter
}
