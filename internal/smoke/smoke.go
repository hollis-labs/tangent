// Package smoke is the deterministic operability check for Tangent's MCP
// deployment: direct `/mcp`, legacy `/sse`, the Tether gateway that fronts
// them, and the Cerberus resource that owns the process.
//
// # Why it exists
//
// Every consumer of Tangent's MCP surface reaches it through a different
// number of hops. A local client POSTs `/mcp`. An older client opens `/sse`.
// An agent behind Tether reaches a gateway that reads a catalog entry, dials
// `/sse`, and re-advertises what it found. Cerberus owns whether the process
// runs at all. Four hops means four ways to be broken, and the failure they
// produce at the far end is the same shrug: "Tangent isn't working."
//
// That shrug costs an incident. The four causes need four different fixes —
// restart the process, restart the gateway, refresh the catalog, investigate
// one capability — and a check that returns a single non-zero exit tells an
// operator none of them. So every finding this package produces names one of
// four modes, and the mode is the first token on the line.
//
// # The four modes
//
//   - [ModeProcessDown]: nothing is serving. Includes the case a supervisor
//     query alone misses — Cerberus reports `running` from supervisor state,
//     not from a live probe, so `running` and `nothing is listening` are
//     compatible answers and only a probe separates them.
//   - [ModeUpstreamAbsent]: the gateway is up but Tangent is not among what it
//     serves — no catalog entry, a disabled one, or a discovery that returns
//     no Tangent tools.
//   - [ModeCatalogStale]: something is answering, but the surface it
//     advertises is not the surface this build ships. A deployed binary older
//     than the artifact on disk, or a gateway serving a cached tool list.
//   - [ModeCapabilityUnhealthy]: the process serves and the surface matches,
//     but readiness or a per-kind capability report says it cannot do the
//     work.
//
// # Never a hard-coded tool count
//
// The expected tool surface is derived by running the binary under test and
// asking it, never by comparing against a number written down here. That
// number has been wrong in this repository's own documentation more times than
// it has been right — it moved from 25 to 46 across a single day of tasks —
// and a smoke check whose reference is a stale literal reports drift that
// isn't there while missing the drift that is. [Surface] is always produced by
// [NewSurface] from a live `tools/list`, and the only comparison this package
// makes is between two observed surfaces: the one the shipped build advertises
// and the one something further down the chain does.
package smoke

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/tangent/internal/health"
)

// Mode names one of the four ways an MCP deployment fails. It is the first
// token of a finding line because it is the only part an operator needs to
// read to know which thing to go touch.
type Mode string

const (
	// ModeProcessDown means nothing is serving MCP at the address checked.
	ModeProcessDown Mode = "PROCESS_DOWN"
	// ModeUpstreamAbsent means the gateway is reachable but Tangent is not
	// among the upstreams it serves.
	ModeUpstreamAbsent Mode = "UPSTREAM_ABSENT"
	// ModeCatalogStale means something answered with a surface that is not the
	// one this build ships.
	ModeCatalogStale Mode = "CATALOG_STALE"
	// ModeCapabilityUnhealthy means the process serves but reports that it
	// cannot do the work.
	ModeCapabilityUnhealthy Mode = "CAPABILITY_UNHEALTHY"
)

// Finding is one failed check, classified.
//
// Detail says what was observed and Action says what to do about it. Both are
// required: "sse: 25 tools, want 46" is a symptom, and an operator reading it
// at 2am still has to work out that the gateway is serving a cached list.
type Finding struct {
	Mode   Mode
	Check  string
	Detail string
	Action string
}

// String renders the operator-facing line. The format is fixed so it can be
// grepped: `FAIL [MODE] check: detail`, then the action indented beneath.
func (f Finding) String() string {
	return fmt.Sprintf("FAIL [%s] %s: %s\n  operator action: %s", f.Mode, f.Check, f.Detail, f.Action)
}

// Findings is an ordered set of failures from one smoke run.
type Findings []Finding

// Add appends a finding when one was produced. Nil is the pass case, so every
// classifier in this package returns *Finding and callers stay branch-free.
func (f *Findings) Add(finding *Finding) {
	if finding != nil {
		*f = append(*f, *finding)
	}
}

// Modes lists the distinct modes observed, in the order the constants are
// declared, so a summary line reads the same way every run.
func (f Findings) Modes() []Mode {
	seen := map[Mode]bool{}
	for _, finding := range f {
		seen[finding.Mode] = true
	}
	var modes []Mode
	for _, mode := range []Mode{ModeProcessDown, ModeUpstreamAbsent, ModeCatalogStale, ModeCapabilityUnhealthy} {
		if seen[mode] {
			modes = append(modes, mode)
		}
	}
	return modes
}

// String renders every finding, newest last, with a summary of the modes.
func (f Findings) String() string {
	if len(f) == 0 {
		return "PASS"
	}
	lines := make([]string, 0, len(f)+1)
	for _, finding := range f {
		lines = append(lines, finding.String())
	}
	modes := make([]string, 0, 4)
	for _, mode := range f.Modes() {
		modes = append(modes, string(mode))
	}
	lines = append(lines, fmt.Sprintf("%d finding(s); modes: %s", len(f), strings.Join(modes, ", ")))
	return strings.Join(lines, "\n")
}

// Surface is a tool surface as some endpoint actually advertised it.
//
// There is deliberately no constructor that takes a count. A surface only ever
// comes from a `tools/list` that really happened.
type Surface struct {
	// Names is sorted and de-duplicated.
	Names []string
	// Digest is a short sha256 over the sorted names. It makes two surfaces
	// comparable in a log line without printing forty-six of them.
	Digest string
}

// NewSurface derives a surface from the names an endpoint advertised.
func NewSurface(names []string) Surface {
	unique := make(map[string]bool, len(names))
	for _, name := range names {
		unique[name] = true
	}
	sorted := make([]string, 0, len(unique))
	for name := range unique {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return Surface{Names: sorted, Digest: hex.EncodeToString(sum[:])[:12]}
}

// Count is how many tools the surface carries.
func (s Surface) Count() int { return len(s.Names) }

// Describe is the one-line form used inside findings.
func (s Surface) Describe() string {
	return fmt.Sprintf("%d tools (digest %s)", s.Count(), s.Digest)
}

// Diff reports the names the other surface is missing and the ones it adds.
func (s Surface) Diff(other Surface) (missing, extra []string) {
	have := map[string]bool{}
	for _, name := range other.Names {
		have[name] = true
	}
	for _, name := range s.Names {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	want := map[string]bool{}
	for _, name := range s.Names {
		want[name] = true
	}
	for _, name := range other.Names {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	return missing, extra
}

// bounded truncates a name list so one finding stays readable when a whole
// surface has moved.
func bounded(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:limit], ", ") + fmt.Sprintf(", …(+%d)", len(names)-limit)
}

// CompareSurface classifies a difference between the surface the shipped build
// advertises and the surface something further down the chain does.
//
// A difference is always [ModeCatalogStale] rather than a generic mismatch:
// the surface is generated from the binary, so if two things that should be
// serving the same binary disagree, one of them is serving something older —
// a deployed process behind the artifact on disk, or a gateway answering from
// a cached list.
func CompareSurface(reference, observed Surface, where string) *Finding {
	if reference.Digest == observed.Digest {
		return nil
	}
	missing, extra := reference.Diff(observed)
	detail := fmt.Sprintf("shipped build advertises %s, %s advertises %s",
		reference.Describe(), where, observed.Describe())
	if len(missing) > 0 {
		detail += fmt.Sprintf("; absent there: %s", bounded(missing, 8))
	}
	if len(extra) > 0 {
		detail += fmt.Sprintf("; only there: %s", bounded(extra, 8))
	}
	return &Finding{
		Mode:   ModeCatalogStale,
		Check:  where + " tool surface",
		Detail: detail,
		Action: "Redeploy Tangent so the serving process is the build on disk (`tangent --migrate-only` " +
			"first when the schema moved), then refresh the gateway's catalog so it re-lists tools.",
	}
}

// Unreachable classifies a transport that could not be reached at all.
func Unreachable(check, address string, cause error) *Finding {
	return &Finding{
		Mode:   ModeProcessDown,
		Check:  check,
		Detail: fmt.Sprintf("no MCP answer at %s: %v", address, cause),
		Action: "Start or restart the Tangent process (`cerberus resource deploy tangent-dev` when it is " +
			"managed, `./tangent` when it is not), then re-run this check.",
	}
}

// SupervisorState is what a supervisor says about the resource, and what a
// probe of the address it advertises actually found.
type SupervisorState struct {
	Resource string
	// Status is the supervisor's own word for the resource — `running`,
	// `stopped`, and so on. It is supervisor bookkeeping, not a probe.
	Status string
	// Listening is whether MCP actually answered at the advertised address.
	Listening bool
	Address   string
}

// ClassifySupervisor catches the discrepancy a supervisor query alone cannot
// see.
//
// `cerberus resource status` reports the state the supervisor believes it put
// the resource in. It does not dial the port. So a process that exited without
// the supervisor noticing, or one that is up but never bound its listener,
// still reads `running` — and an operator who trusts that answer spends the
// first twenty minutes of an incident looking somewhere else. This is the
// check that makes the two answers disagree out loud.
func ClassifySupervisor(state SupervisorState) *Finding {
	running := strings.EqualFold(strings.TrimSpace(state.Status), "running")
	switch {
	case running && !state.Listening:
		return &Finding{
			Mode:  ModeProcessDown,
			Check: "supervisor vs listener",
			Detail: fmt.Sprintf(
				"supervisor reports %s status=%q but nothing answers MCP at %s; supervisor status is "+
					"bookkeeping, not a live probe, so the two can disagree",
				state.Resource, state.Status, state.Address),
			Action: fmt.Sprintf("Restart the process rather than trusting the supervisor: "+
				"`cerberus resource deploy %s`, then re-run this check.", state.Resource),
		}
	case !running && !state.Listening:
		return &Finding{
			Mode:   ModeProcessDown,
			Check:  "supervisor vs listener",
			Detail: fmt.Sprintf("supervisor reports %s status=%q and nothing answers MCP at %s", state.Resource, state.Status, state.Address),
			Action: fmt.Sprintf("Start the resource: `cerberus resource deploy %s`.", state.Resource),
		}
	case !running && state.Listening:
		return &Finding{
			Mode:  ModeCatalogStale,
			Check: "supervisor vs listener",
			Detail: fmt.Sprintf(
				"MCP answers at %s but the supervisor reports %s status=%q, so the serving process is not "+
					"the one the supervisor owns and a deploy will not replace it",
				state.Address, state.Resource, state.Status),
			Action: fmt.Sprintf("Find the unmanaged process holding the port and stop it by pid, then "+
				"`cerberus resource deploy %s` so the managed build is the one serving.", state.Resource),
		}
	}
	return nil
}

// CatalogEntry is a Tether catalog entry for one MCP upstream, as read from
// disk. Found is separate from the rest because "no entry" and "an entry that
// is wrong" are different incidents.
type CatalogEntry struct {
	ID        string
	Found     bool
	Enabled   bool
	Transport string
	URL       string
	Path      string
}

// ReadCatalogEntry reads one `<root>/mcp-servers/<id>.yaml` entry.
//
// It parses the four fields this check cares about by hand rather than pulling
// in a YAML dependency for a five-line file, and it never writes: an operator
// running a smoke check must not thereby modify the catalog whose staleness is
// the thing being measured.
func ReadCatalogEntry(root, id string) (CatalogEntry, error) {
	path := filepath.Join(root, "mcp-servers", id+".yaml")
	entry := CatalogEntry{ID: id, Path: path}
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is composed from an operator-supplied catalog root and a fixed id.
	if os.IsNotExist(err) {
		return entry, nil
	}
	if err != nil {
		return entry, fmt.Errorf("read catalog entry: %w", err)
	}
	entry.Found = true
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "transport":
			entry.Transport = value
		case "url":
			entry.URL = value
		case "enabled":
			entry.Enabled = value == "true"
		}
	}
	return entry, nil
}

// ClassifyCatalogEntry checks the entry a gateway would resolve against the
// address that is actually serving.
//
// A missing or disabled entry is [ModeUpstreamAbsent] — the gateway is fine,
// Tangent simply is not in it. An entry that points somewhere else is
// [ModeCatalogStale]: the gateway will dial and get nothing, or get something
// that is not this deployment.
func ClassifyCatalogEntry(entry CatalogEntry, servingURL string) *Finding {
	switch {
	case !entry.Found:
		return &Finding{
			Mode:   ModeUpstreamAbsent,
			Check:  "tether catalog entry",
			Detail: fmt.Sprintf("no catalog entry for %q at %s", entry.ID, entry.Path),
			Action: "Add the Tangent upstream to the Tether catalog (`transport: sse`, " +
				"`url: <base>/sse`, `enabled: true`) and refresh the gateway.",
		}
	case !entry.Enabled:
		return &Finding{
			Mode:   ModeUpstreamAbsent,
			Check:  "tether catalog entry",
			Detail: fmt.Sprintf("catalog entry %q is present but disabled", entry.ID),
			Action: "Set `enabled: true` in the catalog entry and refresh the gateway.",
		}
	case entry.Transport != "sse":
		return &Finding{
			Mode:  ModeCatalogStale,
			Check: "tether catalog entry",
			Detail: fmt.Sprintf("catalog entry %q declares transport %q; Tangent publishes the legacy "+
				"stream at /sse for this gateway", entry.ID, entry.Transport),
			Action: "Set `transport: sse` in the catalog entry and refresh the gateway.",
		}
	case !strings.HasPrefix(entry.URL, strings.TrimSuffix(servingURL, "/")):
		return &Finding{
			Mode:   ModeCatalogStale,
			Check:  "tether catalog entry",
			Detail: fmt.Sprintf("catalog entry %q points at %s but MCP is serving at %s", entry.ID, entry.URL, servingURL),
			Action: "Point the catalog entry's `url` at the address that is serving, then refresh the gateway.",
		}
	}
	return nil
}

// ClassifyGatewayDiscovery classifies what a gateway advertised for Tangent.
//
// Zero tools is [ModeUpstreamAbsent] rather than a stale surface: the gateway
// answered, so it is up, and it has nothing from this upstream to offer. That
// is the shape of "Tangent disappeared from mux".
func ClassifyGatewayDiscovery(reference, observed Surface, gateway string) *Finding {
	if observed.Count() == 0 {
		return &Finding{
			Mode:  ModeUpstreamAbsent,
			Check: gateway + " discovery",
			Detail: fmt.Sprintf("%s answered tools/list but advertised no Tangent tools; the shipped build "+
				"has %s", gateway, reference.Describe()),
			Action: "Check that the gateway's catalog still carries the Tangent upstream and that it can " +
				"reach it, then refresh the gateway's catalog.",
		}
	}
	return CompareSurface(reference, observed, gateway)
}

// ClassifyReadiness turns a readiness report into a capability finding.
//
// Only a failed check becomes a finding. A `warn` does not, and that is
// internal/health's position rather than a shortcut taken here: degraded means
// the process is serving, and an operator paged for every warning stops
// reading them. Degraded checks are still visible — [DegradedChecks] hands
// them to the caller to report alongside a passing run.
//
// The finding reuses the report's own operator action rather than writing a
// second one. internal/health already decided what to do about a failed
// database or a schema behind the binary; restating it here is how two answers
// to one question start to drift.
func ClassifyReadiness(report health.ReadinessReport, where string) *Finding {
	for _, check := range report.Checks {
		if check.Status != StatusFailed {
			continue
		}
		action := check.Action
		if action == "" {
			action = "Read the full report at " + where + "/readyz."
		}
		return &Finding{
			Mode:   ModeCapabilityUnhealthy,
			Check:  "readiness: " + check.Name,
			Detail: fmt.Sprintf("%s — %s", check.Status, check.Detail),
			Action: action,
		}
	}
	return nil
}

// StatusFailed is internal/health's failed verdict, named here so this
// package's one comparison against it reads at the call site.
const StatusFailed = health.StatusFail

// DegradedChecks names the checks that warned. They are reported, never
// escalated: a run whose only news is a warning still passes.
func DegradedChecks(report health.ReadinessReport) []string {
	var degraded []string
	for _, check := range report.Checks {
		if check.Status == health.StatusWarn {
			degraded = append(degraded, fmt.Sprintf("%s: %s", check.Name, check.Detail))
		}
	}
	return degraded
}

// ClassifyCapabilitySummary turns the per-kind summary into a finding naming
// the kinds that cannot be served.
func ClassifyCapabilitySummary(report health.CapabilitySummaryReport, where string) *Finding {
	if len(report.Unusable) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(report.Unusable))
	for _, unusable := range report.Unusable {
		kinds = append(kinds, fmt.Sprintf("%s(%s)", unusable.Kind, unusable.State))
	}
	action := report.Action
	if action == "" {
		action = "Query the kind at " + where + "/healthz/capability/{kind} for its materialization state."
	}
	return &Finding{
		Mode:  ModeCapabilityUnhealthy,
		Check: "capability health",
		Detail: fmt.Sprintf("%d of %d managed definitions are unusable: %s",
			len(report.Unusable), report.ManagedDefinitions, bounded(kinds, 8)),
		Action: action,
	}
}
