package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
)

// Lifecycle operations sweep registrations by exact load owner. Enable intent
// is host-owned and independent of plugin configuration; live surfaces reflect
// removal immediately, while pinned interaction material remains retained.

// ErrPluginNotLoaded reports an Unload naming a plugin this host does not hold.
var ErrPluginNotLoaded = errors.New("pluginhost: plugin is not loaded")

// loadAttempt is the host's own record of one Load call. It is the host's
// bookkeeping and not the plugin's self-report: `Refused` is true when Load
// returned an error, whatever the plugin's own Status says afterwards.
type loadAttempt struct {
	id      string
	name    string
	version string
	at      time.Time
	err     string
	// p is nil for a refused attempt. It is held so Inventory can ask a loaded
	// plugin for the status fields only it knows.
	p plugin.Plugin
}

// recordAttempt files one Load call. A refusal is recorded under whatever id
// the host managed to read, which may be empty if the plugin could not even
// name itself — identify turns that into something printable rather than a
// blank row.
func (h *Host) recordAttempt(p plugin.Plugin, id string, err error) {
	attempt := &loadAttempt{id: id, at: time.Now().UTC(), p: p}
	if err != nil {
		attempt.err = err.Error()
	}
	if p != nil {
		attempt.name = safeString(p.Name)
		attempt.version = safeString(p.Version)
	}
	h.mu.Lock()
	h.attempts = append(h.attempts, attempt)
	if err == nil {
		if owner := h.owners[id]; owner != nil {
			owner.ready = true
			owner.attempt = attempt
		}
	}
	h.mu.Unlock()
}

// RecordUnusable records a plugin that was installed and never reached Load —
// a manifest that would not parse, a protocol this host does not speak, an
// entrypoint that is not there.
//
// It exists because those failures have no plugin object to attribute them to,
// and the health inventory is where "which plugins loaded, which refused and
// why" is answered (CW-20260910-0036). An install that cannot run is more
// confusing than one that is absent: the operator put it there. Reporting it
// as a refusal with a reason is the difference between "why is my plugin not
// working" and one line that says.
//
// The id is the directory, because a plugin whose manifest would not parse has
// not told us any other name.
func (h *Host) RecordUnusable(dir string, reason error) {
	attempt := &loadAttempt{id: dir, at: time.Now().UTC()}
	if reason != nil {
		attempt.err = reason.Error()
	}
	h.mu.Lock()
	h.attempts = append(h.attempts, attempt)
	h.mu.Unlock()
}

// identify renders a plugin id for an error message when the id may be empty.
func identify(id string) string {
	if id == "" {
		return "(unnamed plugin)"
	}
	return id
}

// safeString calls a plugin's own accessor without letting it take the process
// down. A plugin that panics describing itself is a plugin defect; it is not a
// reason for a health report to be unavailable.
func safeString(read func() string) (value string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = "(panicked)"
		}
	}()
	return read()
}

// Unload drops one plugin from this host's roster and calls its Unload so it
// can release its own state.
//
// The load owner is fenced and its registrations swept before plugin teardown.
// Unattributed direct host registrations remain host-owned.
func (h *Host) Unload(id string) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	return h.unload(id)
}

func (h *Host) unload(id string) error {
	h.mu.Lock()
	p, loaded := h.loaded[id]
	if !loaded {
		h.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPluginNotLoaded, id)
	}
	owner := h.owners[id]
	if owner != nil {
		owner.cancel()
	}
	delete(h.loaded, id)
	delete(h.owners, id)
	for name, held := range h.toolOwners {
		if held == owner {
			delete(h.tools, name)
			delete(h.toolOwners, name)
			if h.toolRegistry != nil {
				h.toolRegistry.RemovePluginTools(name)
			}
		}
	}
	for name, held := range h.routeOwners {
		if held == owner {
			delete(h.routes, name)
			delete(h.routeOwners, name)
		}
	}
	var errs []error
	for name, held := range h.kindOwners {
		if held == owner {
			if err := h.envSvc.RemoveContributedKind(name); err != nil {
				errs = append(errs, err)
			}
			delete(h.kinds, name)
			delete(h.kindOwners, name)
		}
	}
	h.mu.Unlock()
	if err := unloadSafely(p); err != nil {
		errs = append(errs, fmt.Errorf("pluginhost: unload %s: %w", id, err))
	}
	return errors.Join(errs...)
}

// UnloadAll releases every in-flight dispatch and unloads every plugin.
//
// The release comes first and it is the whole point of the ordering: a plugin
// whose handler is mid-call would otherwise be unloaded underneath it, and a
// shutdown that waited for a hung handler's full dispatch budget would be the
// CW-20260909-0045 failure with a different name — a process that will not hand
// over its database because something nobody bounded is still running.
//
// Errors are collected rather than returned at the first one. This runs on a
// shutdown path, where one plugin's failure to tidy up must not stop the next
// plugin from getting the chance.
//
// It is idempotent: a second call unloads nothing and closes nothing.
func (h *Host) UnloadAll() error {
	h.mu.Lock()
	if h.unloaded {
		h.mu.Unlock()
		return nil
	}
	h.unloaded = true
	close(h.release)
	ids := make([]string, 0, len(h.loaded))
	for id := range h.loaded {
		ids = append(ids, id)
	}
	h.mu.Unlock()

	// Reverse load order, so a plugin is unloaded before anything it declared a
	// dependency on. Load order is the shipped list's order; sorting by it here
	// would need bookkeeping the host does not otherwise keep, so this sorts by
	// attempt order, which is the same thing.
	ordered := h.attemptOrder(ids)
	var errs []error
	for i := len(ordered) - 1; i >= 0; i-- {
		if err := h.Unload(ordered[i]); err != nil && !errors.Is(err, ErrPluginNotLoaded) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// attemptOrder puts ids back into the order they were loaded in. An id with no
// recorded attempt — which should not happen, but a shutdown path is the wrong
// place to assume — sorts last, alphabetically, so the answer is deterministic.
func (h *Host) attemptOrder(ids []string) []string {
	h.mu.Lock()
	position := map[string]int{}
	for index, attempt := range h.attempts {
		if _, seen := position[attempt.id]; !seen {
			position[attempt.id] = index
		}
	}
	h.mu.Unlock()

	ordered := make([]string, len(ids))
	copy(ordered, ids)
	sort.Slice(ordered, func(i, j int) bool {
		left, leftKnown := position[ordered[i]]
		right, rightKnown := position[ordered[j]]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if !leftKnown {
			return ordered[i] < ordered[j]
		}
		return left < right
	})
	return ordered
}

// unloadSafely calls a plugin's Unload without letting a defect in it take down
// a shutdown. The whole reason to unload on the way out is to let plugins
// release what they hold; a panicking one must not stop the others.
func unloadSafely(p plugin.Plugin) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panicked: %v", recovered)
		}
	}()
	return p.Unload()
}

// PluginRecord is one plugin as this host knows it.
//
// Loaded, Enabled and State are host-owned facts. Successful startup, desired
// enabled intent and current availability remain separate, so a failed or
// quarantined plugin cannot report itself ready.
type PluginRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// Loaded is the host's answer: Load was called and returned nil, and the
	// plugin has not been unloaded since.
	Loaded bool `json:"loaded"`
	// Enabled is durable host intent, independent of current availability.
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	// At is when the host recorded the load attempt, successful or not.
	At time.Time `json:"at"`
	// Error describes an initial refused load only. RuntimeError describes
	// unavailability after successful load.
	Error           string `json:"error,omitempty"`
	FailedAfterLoad bool   `json:"failed_after_load,omitempty"`
	RuntimeError    string `json:"runtime_error,omitempty"`

	// Restarts is how many automatic restart attempts this plugin has made
	// (CW-20260911-0068), successful or not. Zero for a plugin that has never
	// crashed and for a compiled-in plugin, which has no process to restart —
	// the two are indistinguishable here on purpose: "never needed a restart"
	// is the only fact this field states.
	Restarts  int  `json:"restarts,omitempty"`
	Exhausted bool `json:"exhausted,omitempty"`

	// Healthy is the last plugin/health verdict this host has cached for this
	// plugin (CW-20260911-0069), or nil if it has never been probed — which is
	// the normal state until something asks for a health report. A pointer
	// rather than a bare bool because the important case is Healthy pointing
	// at false, and omitempty on a bare bool would hide exactly that.
	Healthy *bool `json:"healthy,omitempty"`
	// HealthMessage is the plugin's own message on its last health answer, or
	// this host's account of why the probe itself failed (a timeout, a dead
	// process). Empty when Healthy is true or nil.
	HealthMessage string `json:"health_message,omitempty"`
	// HealthCheckedAt is when the cached verdict above was obtained. Zero when
	// Healthy is nil.
	HealthCheckedAt time.Time `json:"health_checked_at,omitempty"`
}

// healthProber is a host-local optional interface, the same pattern
// HealthChecker is for the SDK's plugin-side surface: a compiled-in plugin
// has no process to probe, so it simply does not implement this, and
// Inventory treats that exactly like "not probed yet" rather than an error.
type healthProber interface {
	probeHealth(ctx context.Context) pluginHealth
}

// restartReporter is the same pattern for CW-20260911-0068's restart count —
// declared where it is consumed rather than on plugin.Plugin, because only a
// subprocess plugin can ever answer it.
type restartReporter interface {
	Restarts() int
}

// PluginInventory is what this host loaded and what it refused.
//
// The contributed lists aggregate the current surface. Exact owner identity is
// maintained internally for removal; direct host registrations stay host-owned.
type PluginInventory struct {
	Loaded  int `json:"loaded"`
	Refused int `json:"refused"`

	Plugins []PluginRecord `json:"plugins"`

	// ContributedKinds, Tools and Routes are the surfaces plugins added to this
	// host, sorted, across all of them.
	ContributedKinds []string `json:"contributed_kinds"`
	Tools            []string `json:"contributed_tools"`
	Routes           []string `json:"contributed_routes"`
	// Attribution states in the report itself that the three lists above are
	// host-wide. A consumer reading this document during an incident should not
	// have to find this file to learn it.
	Attribution string `json:"attribution"`
}

// attributionNote is the one sentence every inventory carries.
const attributionNote = "plugin Load receives an owner-scoped host; registrations are swept by exact load owner, while direct host registrations remain host-owned"

// Inventory reports which plugins loaded, which refused and why, and what they
// contributed between them.
//
// It exists so `tangent.health_report` can answer that question directly rather
// than leaving an operator to infer it from a tool list — which is inference
// from the wrong evidence: a plugin can load and contribute no tool at all,
// which is exactly what the kind-contributing plugin in this build does.
//
// It takes a context because a loaded subprocess plugin is asked plugin/health
// as part of building this document (CW-20260911-0069) — on demand, exactly
// here and nowhere else in this host, which is the whole reason readiness's
// existing probeBudget bounds this call too rather than a separate one being
// invented for it. Every plugin implementing the probe is asked concurrently,
// so the wall-clock cost of N plugins is the slowest one, not the sum.
func (h *Host) Inventory(ctx context.Context) PluginInventory {
	h.mu.Lock()
	attempts := make([]*loadAttempt, len(h.attempts))
	copy(attempts, h.attempts)
	stillLoaded := map[string]bool{}
	loading := map[string]bool{}
	currentAttempts := map[string]*loadAttempt{}
	for id := range h.loaded {
		owner := h.owners[id]
		stillLoaded[id] = owner != nil && owner.ready
		loading[id] = !stillLoaded[id]
		if stillLoaded[id] {
			currentAttempts[id] = owner.attempt
		}
	}
	intent := map[string]bool{}
	faults := map[string]string{}
	for id := range h.factories {
		intent[id] = true
	}
	for id, enabled := range h.intent {
		intent[id] = enabled
	}
	for id, fault := range h.faults {
		faults[id] = fault
	}
	h.mu.Unlock()
	// Inventory is current state, not a repetition of every historical attempt.
	latest := map[string]*loadAttempt{}
	for _, attempt := range attempts {
		latest[attempt.id] = attempt
	}
	for id, attempt := range currentAttempts {
		if attempt != nil {
			latest[id] = attempt
		}
	}
	for id := range intent {
		if latest[id] == nil {
			latest[id] = &loadAttempt{id: id}
		}
	}
	attempts = attempts[:0]
	for _, attempt := range latest {
		attempts = append(attempts, attempt)
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].id < attempts[j].id })

	health := probeHealthConcurrently(ctx, attempts, stillLoaded)

	inventory := PluginInventory{
		Plugins:          make([]PluginRecord, 0, len(attempts)),
		ContributedKinds: h.ContributedKinds(),
		Attribution:      attributionNote,
	}
	for _, tool := range h.MCPTools() {
		inventory.Tools = append(inventory.Tools, tool.Name)
	}
	for _, route := range h.HTTPRoutes() {
		inventory.Routes = append(inventory.Routes, route.Pattern())
	}
	if inventory.Tools == nil {
		inventory.Tools = []string{}
	}
	if inventory.Routes == nil {
		inventory.Routes = []string{}
	}

	for _, attempt := range attempts {
		record := PluginRecord{
			ID:      identify(attempt.id),
			Name:    attempt.name,
			Version: attempt.version,
			Loaded:  attempt.err == "" && stillLoaded[attempt.id],
			At:      attempt.at,
			Error:   attempt.err,
		}
		if record.Loaded && attempt.p != nil {
			record.Enabled = true
			record.Restarts = safeRestarts(attempt.p)
			record.Exhausted = safeExhausted(attempt.p)
			if reporter, ok := attempt.p.(interface{ RuntimeFailure() string }); ok {
				record.RuntimeError = reporter.RuntimeFailure()
				record.FailedAfterLoad = record.RuntimeError != ""
			}
		}
		if probed, wasProbed := health[attempt.id]; wasProbed {
			healthy := probed.ok
			record.Healthy = &healthy
			record.HealthMessage = probed.message
			record.HealthCheckedAt = probed.checked
		}
		if enabled, known := intent[attempt.id]; known {
			record.Enabled = enabled
		}
		record.State = "disabled"
		if record.Enabled {
			record.State = "stopped"
			if attempt.err != "" {
				record.State = "failed"
			}
		}
		if record.Loaded {
			record.State = "running"
		}
		if record.Enabled && record.FailedAfterLoad {
			record.State = "failed"
		}
		if loading[attempt.id] {
			record.State = "loading"
		}
		if fault := faults[attempt.id]; fault != "" {
			record.RuntimeError = fault
			record.FailedAfterLoad = true
			if record.Enabled {
				record.State = "quarantined"
			}
		}
		if attempt.err != "" {
			inventory.Refused++
		} else if record.Loaded {
			inventory.Loaded++
		}
		inventory.Plugins = append(inventory.Plugins, record)
	}
	return inventory
}

// probeHealthConcurrently asks every still-loaded plugin that implements
// healthProber for its cached verdict, in parallel. A plugin that does not
// implement it (every compiled-in plugin, and any subprocess plugin this
// host has not yet loaded) is simply absent from the result — Inventory reads
// that as "never probed," not as unhealthy.
func probeHealthConcurrently(
	ctx context.Context, attempts []*loadAttempt, stillLoaded map[string]bool,
) map[string]pluginHealth {
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out = map[string]pluginHealth{}
	)
	for _, attempt := range attempts {
		if attempt.p == nil || !stillLoaded[attempt.id] {
			continue
		}
		prober, ok := attempt.p.(healthProber)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, prober healthProber) {
			defer wg.Done()
			result := safeProbeHealth(ctx, prober)
			mu.Lock()
			out[id] = result
			mu.Unlock()
		}(attempt.id, prober)
	}
	wg.Wait()
	return out
}

// safeProbeHealth calls a plugin's health probe without letting a defect in
// it fail the whole inventory.
func safeProbeHealth(ctx context.Context, prober healthProber) (result pluginHealth) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = pluginHealth{ok: false, message: fmt.Sprintf("panicked: %v", recovered), checked: time.Now().UTC()}
		}
	}()
	return prober.probeHealth(ctx)
}

// safeRestarts reads a plugin's restart count without letting a defect in it
// fail the whole inventory.
func safeRestarts(p plugin.Plugin) (count int) {
	reporter, ok := p.(restartReporter)
	if !ok {
		return 0
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			count = 0
		}
	}()
	return reporter.Restarts()
}

func safeExhausted(p plugin.Plugin) (exhausted bool) {
	defer func() {
		if recover() != nil {
			exhausted = false
		}
	}()
	if reporter, ok := p.(interface{ Exhausted() bool }); ok {
		return reporter.Exhausted()
	}
	return false
}
