package health

import (
	"context"
	"strconv"
	"strings"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// Readiness answers "can this process serve traffic right now".
//
// It runs the dependency checks in a fixed order and under a fixed time
// budget. The order is the order an operator would debug in — durable state
// first, then the schema over it, then what the process loaded, then which
// plugins extended it, then what a browser would receive, then what happens to
// an answer once it is given — so the first failing line is usually the cause
// and not a consequence.
func (r *Reporter) Readiness(ctx context.Context) ReadinessReport {
	ctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()

	report := ReadinessReport{Probe: ProbeReadiness}
	databaseCheck := r.checkDatabase(ctx)
	report.Checks = []Check{
		databaseCheck,
		// The schema check is skipped rather than guessed when the database
		// itself is down: reporting "migrations: unknown version" alongside
		// "database: unreachable" is two lines for one fact, and the second
		// one invites an operator to run a migration against a database that
		// is not there.
		r.checkMigrations(ctx, databaseCheck.Status == StatusFail),
		r.checkDefinitionRegistry(),
		// Immediately after the registry, because a plugin-contributed kind is
		// in that registry and a refused plugin is why one would be missing
		// from it (ADR 0007 §4).
		r.checkPlugins(ctx),
		r.checkRendererHost(),
		r.checkDeliveryWorker(),
	}
	report.Status = summarize(report.Checks)
	// Criterion 5: a failure names where the rest of its story is. The link is
	// attached after the checks are built rather than inside each one, so a new
	// check cannot be added without acquiring it.
	for i := range report.Checks {
		if report.Checks[i].Status != StatusPass {
			report.Checks[i].Correlation = correlationForCheck(report.Checks[i].Name)
		}
	}
	r.observeChecks(ctx, report)
	return report
}

// summarize folds the checks into one verdict. Any failure is unavailable; any
// warning without a failure is degraded.
func summarize(checks []Check) Summary {
	summary := SummaryOK
	for _, check := range checks {
		switch check.Status {
		case StatusFail:
			return SummaryUnavailable
		case StatusWarn:
			summary = SummaryDegraded
		case StatusPass:
		}
	}
	return summary
}

func (r *Reporter) checkDatabase(ctx context.Context) Check {
	if r.db == nil {
		return Check{
			Name:   CheckDatabase,
			Status: StatusFail,
			Detail: "no durable-state handle is installed in this process",
			Action: "This binary was constructed without a database. Nothing an operator " +
				"can do at runtime fixes it; report the build as defective." + r.redeployHint(),
		}
	}
	if err := r.db.PingContext(ctx); err != nil {
		return Check{
			Name:   CheckDatabase,
			Status: StatusFail,
			// The driver error is deliberately not echoed: on SQLite it
			// carries the database file path, and a probe response is the
			// wrong place to publish where durable state lives.
			Detail: "the durable-state handle did not answer a ping",
			Action: "Stop sending work to this process: it is serving HTTP but cannot read " +
				"or write durable state. Confirm the SQLite file named by TANGENT_DB_PATH " +
				"exists and is writable by this user, then restart." + r.redeployHint(),
		}
	}
	// A ping on a pooled handle can succeed against a connection that a
	// closed or read-only file would fail on the first statement, so readiness
	// asks for one trivial row rather than trusting the ping alone.
	var one int
	if err := r.db.QueryRowContext(ctx, `SELECT 1;`).Scan(&one); err != nil || one != 1 {
		return Check{
			Name:   CheckDatabase,
			Status: StatusFail,
			Detail: "the durable-state handle answered a ping but could not execute a statement",
			Action: "Stop sending work to this process. The database file is present but " +
				"unusable — check for a stale WAL, a read-only mount, or a full disk, " +
				"then restart." + r.redeployHint(),
		}
	}
	return Check{Name: CheckDatabase, Status: StatusPass, Detail: "reachable and answering statements"}
}

func (r *Reporter) checkMigrations(ctx context.Context, databaseDown bool) Check {
	if databaseDown {
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "not evaluated: the durable-state handle is unavailable",
			Action: "Fix the database check first; the schema version cannot be read until it passes.",
		}
	}
	status, err := tangentdb.InspectMigrations(ctx, r.db)
	if err != nil {
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "the schema version could not be read",
			Action: "The database answered but its migration bookkeeping is unreadable. " +
				"Take a copy of the database file before doing anything else, then run " +
				"`tangent --migrate-only` and read its output." + r.redeployHint(),
		}
	}
	expected := strconv.FormatInt(status.Expected, 10)
	switch {
	case status.Dirty:
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "schema is marked dirty at version " + strconv.FormatInt(status.Applied, 10) +
				"; a migration failed part-way",
			Action: "Do not serve traffic. Roll the half-applied migration back with " +
				"`tangent --rollback-one`, then re-apply with `tangent --migrate-only`.",
		}
	case !status.Initialized:
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "the database has never been migrated; this binary expects version " + expected,
			Action: "Apply the schema before serving: run `tangent --migrate-only`, then restart." +
				r.redeployHint(),
		}
	case status.Applied < status.Expected:
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "schema is behind: applied version " + strconv.FormatInt(status.Applied, 10) +
				", this binary expects " + expected,
			Action: "Run `tangent --migrate-only` to bring the schema up to this binary's " +
				"version, then restart." + r.redeployHint(),
		}
	case status.Applied > status.Expected:
		return Check{
			Name:   CheckMigrations,
			Status: StatusFail,
			Detail: "schema is ahead: applied version " + strconv.FormatInt(status.Applied, 10) +
				", this binary expects " + expected,
			Action: "An older binary is running against a newer schema; its queries were " +
				"written for a different shape. Deploy the build that owns this schema " +
				"rather than migrating down." + r.redeployHint(),
		}
	}
	return Check{
		Name:   CheckMigrations,
		Status: StatusPass,
		Detail: "schema at expected version " + expected,
	}
}

func (r *Reporter) checkDefinitionRegistry() Check {
	if r.registry == nil {
		return Check{
			Name:   CheckDefinitionRegistry,
			Status: StatusFail,
			Detail: "no definition registry is installed in this process",
			Action: "This binary was constructed without an envelope registry and can serve " +
				"no interaction. Report the build as defective." + r.redeployHint(),
		}
	}
	materialized := r.registry.MaterializedDefinitions()
	if len(materialized) == 0 {
		return Check{
			Name:   CheckDefinitionRegistry,
			Status: StatusFail,
			Detail: "the registry materialized no interaction definitions",
			Action: "No interaction kind can be served. Rebuild with `make build` — a registry " +
				"this empty means the in-tree definition manifests did not register at boot." +
				r.redeployHint(),
		}
	}
	var available, unservable int
	for _, item := range materialized {
		if item.State.Servable() {
			available++
			continue
		}
		unservable++
	}
	total := strconv.Itoa(len(materialized))
	switch {
	case available == 0:
		return Check{
			Name:   CheckDefinitionRegistry,
			Status: StatusFail,
			Detail: "all " + total + " materialized definitions are unservable",
			Action: "No interaction kind can be served. Read per-kind detail at " +
				"/healthz/capability to see whether they are incompatible, quarantined, or " +
				"disabled; the three need different fixes.",
		}
	case unservable > 0:
		return Check{
			Name:   CheckDefinitionRegistry,
			Status: StatusWarn,
			Detail: strconv.Itoa(unservable) + " of " + total + " definitions are unservable",
			Action: "Some interaction kinds will be refused. Read /healthz/capability for the " +
				"per-kind state and reason before a caller discovers it as a failed workflow.",
		}
	}
	return Check{
		Name:   CheckDefinitionRegistry,
		Status: StatusPass,
		Detail: "all " + total + " materialized definitions are available",
	}
}

func (r *Reporter) checkRendererHost() Check {
	if r.renderer == nil {
		return Check{
			Name:   CheckRendererHost,
			Status: StatusFail,
			Detail: "no renderer-host probe is installed in this process",
			Action: "This binary was constructed without an HTTP layer to host renderers. " +
				"Report the build as defective." + r.redeployHint(),
		}
	}
	host := r.renderer()
	switch {
	case host.Mode == "dev-proxy":
		// Deliberately a warning, not a pass. In dev-proxy mode the renderer
		// host is a separate Vite process this probe does not own, and
		// readiness must not assert something it did not observe.
		return Check{
			Name:   CheckRendererHost,
			Status: StatusWarn,
			Detail: "renderer host is an external dev server; this process serves no bundle",
			Action: "Development configuration. Confirm the Vite dev server is up before " +
				"treating a blank room as a Tangent defect; a production build embeds the " +
				"host instead.",
		}
	case !host.Present:
		return Check{
			Name:   CheckRendererHost,
			Status: StatusFail,
			Detail: bound(host.Detail),
			Action: "Every room will load a placeholder rather than a renderer. Rebuild the " +
				"frontend into the binary with `make build`." + r.redeployHint(),
		}
	case host.Assets == 0:
		return Check{
			Name:   CheckRendererHost,
			Status: StatusWarn,
			Detail: "renderer host document is embedded but carries no built assets",
			Action: "The page will load without its script bundle. Rebuild with `make build` " +
				"and confirm the Vite output landed in the embed.",
		}
	}
	return Check{
		Name:   CheckRendererHost,
		Status: StatusPass,
		Detail: "embedded renderer host present with " + strconv.Itoa(host.Assets) + " assets",
	}
}

func (r *Reporter) checkDeliveryWorker() Check {
	if r.delivery == nil {
		return Check{
			Name:   CheckDeliveryWorker,
			Status: StatusFail,
			Detail: "no delivery-worker probe is installed in this process",
			Action: "This binary was constructed without the durable interaction substrate, " +
				"so a resolved interaction has nowhere to be delivered. Report the build as " +
				"defective." + r.redeployHint(),
		}
	}
	worker := r.delivery()
	if !worker.Authorized {
		return Check{
			Name:   CheckDeliveryWorker,
			Status: StatusFail,
			// This is the failure mode that looks healthiest from outside: the
			// UI works, participants answer, and no caller ever receives an
			// outcome. Naming it explicitly is the whole value of the check.
			Detail: "the installed delivery-worker policy does not admit the in-process " +
				"caller-pull adapter",
			Action: "Participants can answer but no caller will ever receive an outcome. " +
				"This is a build misconfiguration, not an operator error — do not restart in " +
				"a loop; deploy a build whose interaction service installs a delivery-worker " +
				"policy.",
		}
	}
	return Check{
		Name:   CheckDeliveryWorker,
		Status: StatusPass,
		Detail: "in-process caller-pull adapter is authorized to deliver terminal outcomes",
	}
}

// redeployHint appends the one command that restarts this process, when the
// process knows what supervises it. An unmanaged `./tangent` gets no command,
// because inventing one would send an operator to a resource that is not there.
func (r *Reporter) redeployHint() string {
	if r.runtime.ManagedResource == "" {
		return ""
	}
	return " Restart through the runtime that owns this process: `cerberus resource deploy " +
		r.runtime.ManagedResource + "`."
}

// bound truncates a string to the report ceiling. Reports are read in a
// terminal during an incident; an unbounded field is how a probe becomes
// unreadable exactly when it matters.
func bound(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxDetailBytes {
		return text
	}
	// Cut on a rune boundary so the truncation cannot produce invalid UTF-8.
	cut := maxDetailBytes
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
