package health

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Plugin health (CW-20260910-0036).
//
// The question is "which plugins loaded, which refused and why", and before
// this the only way to answer it was to read a tool list and infer. That is
// inference from the wrong evidence twice over: a plugin can load and
// contribute no tool at all — the kind-contributing plugin in this build does
// exactly that — and a plugin that refused to load contributes nothing either,
// so the two are indistinguishable in a tools/list.
//
// It reads as a readiness check rather than as a separate probe because that is
// what it is: which plugins this process loaded is a boot-time fact about
// whether it can serve, in the same class as whether the definition registry
// materialized. The structured inventory rides alongside the checks for the
// same reason RendererHost does — the check says pass or fail, the inventory
// says what was observed.
//
// It carries no configuration and no secret, which costs nothing to guarantee:
// the host holds no plugin config at all (see internal/pluginhost's GetConfig),
// so there is none here to leak.

// PluginRecord is one plugin as the host knows it.
//
// Loaded is the host's own record of whether Load returned; Enabled is the
// plugin's self-report. They are separate fields because a report that blended
// them would let a plugin claim it loaded when the host says it did not.
type PluginRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Loaded  bool   `json:"loaded"`
	Enabled bool   `json:"enabled"`
	// At is when the host recorded the load attempt, successful or not.
	At time.Time `json:"at,omitempty"`
	// Error is why a refused load was refused, bounded like every other
	// explanation in this package. It is the host's error text, not a stack.
	Error           string `json:"error,omitempty"`
	FailedAfterLoad bool   `json:"failed_after_load,omitempty"`
	RuntimeError    string `json:"runtime_error,omitempty"`

	// Restarts is how many automatic restart attempts this plugin has made
	// (CW-20260911-0068). Zero for a plugin that never needed one, which reads
	// the same as "not a subprocess plugin" here — that distinction lives in
	// Loaded/Error, not in this count.
	Restarts  int  `json:"restarts,omitempty"`
	Exhausted bool `json:"exhausted,omitempty"`
	// Healthy is the last plugin/health verdict this host has cached, or nil
	// if it has never been probed (the normal state until a health report is
	// requested). A pointer because the case that matters is Healthy pointing
	// at false, and a bare bool's omitempty would hide exactly that.
	Healthy *bool `json:"healthy,omitempty"`
	// HealthMessage is the plugin's own message, bounded like Error, or this
	// host's account of why the probe itself failed. Empty when Healthy is
	// true or nil.
	HealthMessage string `json:"health_message,omitempty"`
	// HealthCheckedAt is when the cached verdict above was obtained.
	HealthCheckedAt time.Time `json:"health_checked_at,omitempty"`
}

// PluginInventory is what the plugin host loaded and what it refused.
//
// The three contributed lists are host-wide and not attributed to a plugin. The
// plugin SDK passes no caller identity to a registration call, so the host does
// not know which plugin registered which tool; Attribution says so in the
// document, because an operator reading it during an incident should not have
// to find the source to learn it.
type PluginInventory struct {
	Loaded           int            `json:"loaded"`
	Refused          int            `json:"refused"`
	Plugins          []PluginRecord `json:"plugins"`
	ContributedKinds []string       `json:"contributed_kinds"`
	Tools            []string       `json:"contributed_tools"`
	Routes           []string       `json:"contributed_routes"`
	Attribution      string         `json:"attribution,omitempty"`
}

// CheckPlugins is the readiness check name.
const CheckPlugins = "plugins"

// WithPlugins installs the plugin-host inventory probe. It is a function rather
// than a value so a report describes the process now: a plugin unloaded at
// shutdown should read as unloaded, not as whatever it was at boot.
//
// The type is declared here rather than imported from internal/pluginhost so
// this package keeps depending on vocabularies and not on another subsystem's
// lifecycle — the same reason DefinitionRegistry is an interface. The
// composition root does the one-line adaptation.
//
// It takes a context because building the inventory sends plugin/health to
// every loaded subprocess plugin (CW-20260911-0069) — a real round trip, not
// a pure read, and bounded by whatever budget the caller (Readiness, or
// tangent.health_report directly) passes down.
func WithPlugins(probe func(ctx context.Context) PluginInventory) Option {
	return func(r *Reporter) { r.plugins = probe }
}

// checkPlugins folds the inventory into one verdict.
//
// A refusal is a failure, not a warning, and the reasoning is the one this
// package keeps making: LoadShipped has no partial mode, so in a normally
// booted process the refused count is zero and a non-zero one means something
// is serving without a surface it was built to have. A build with no probe
// installed warns rather than fails — "nobody wired the reporter" and "the
// plugins are broken" are different incidents, and conflating them is the
// defect this package exists to close.
//
// An unhealthy plugin (CW-20260911-0069) is a warning, not a failure, at this
// level: it can still mean the process overall serves traffic fine while one
// dependency it proxies to is unreachable, which is degraded rather than
// unavailable — the same distinction SummaryDegraded exists to keep separate
// from SummaryUnavailable. A refusal already outranks it via the early return
// above.
func (r *Reporter) checkPlugins(ctx context.Context) Check {
	if r.plugins == nil {
		return Check{
			Name:   CheckPlugins,
			Status: StatusWarn,
			Detail: "no plugin-host probe is installed in this process",
			Action: "This build reports nothing about plugins. Everything else in this " +
				"report is still accurate; treat a missing plugin surface as unexplained " +
				"rather than as absent." + r.redeployHint(),
		}
	}
	inventory := r.plugins(ctx)
	var failed []string
	for _, record := range inventory.Plugins {
		if record.FailedAfterLoad || record.Exhausted {
			failed = append(failed, record.ID)
		}
	}
	sort.Strings(failed)
	if inventory.Refused > 0 {
		var refused []string
		for _, record := range inventory.Plugins {
			if record.Error != "" && !record.Loaded && !record.FailedAfterLoad {
				refused = append(refused, record.ID)
			}
		}
		sort.Strings(refused)
		detail := fmt.Sprintf("%d plugin(s) refused to load: %s", inventory.Refused, joinBounded(refused))
		if len(failed) > 0 {
			detail += fmt.Sprintf("; %d plugin(s) failed after load: %s", len(failed), joinBounded(failed))
		}
		return Check{
			Name:   CheckPlugins,
			Status: StatusFail,
			Detail: bound(detail),
			Action: "A refused plugin contributes no kind, tool or route, so every surface " +
				"it was built to serve is missing. Read the per-plugin error in this " +
				"report's plugin inventory; a plugin that cannot load normally fails the " +
				"boot, so a process serving with one refused is a build that should not " +
				"have started." + r.redeployHint(),
		}
	}
	if len(failed) > 0 {
		return Check{Name: CheckPlugins, Status: StatusFail,
			Detail: bound(fmt.Sprintf("%d plugin(s) failed after load: %s", len(failed), joinBounded(failed))),
			Action: "Read the runtime_error in the plugin inventory; restore the plugin executable or its dependency before restarting it."}
	}
	if inventory.Loaded == 0 {
		return Check{
			Name:   CheckPlugins,
			Status: StatusWarn,
			Detail: "no plugins are loaded",
			Action: "Every plugin-contributed kind, tool and route is absent. If this build " +
				"ships plugins, it did not load them; if it ships none, this line is " +
				"expected.",
		}
	}
	if unhealthy := unhealthyPlugins(inventory); len(unhealthy) > 0 {
		return Check{
			Name:   CheckPlugins,
			Status: StatusWarn,
			Detail: bound(fmt.Sprintf("%d of %d loaded plugin(s) reported unhealthy: %s",
				len(unhealthy), inventory.Loaded, joinBounded(unhealthy))),
			Action: "The plugin is loaded and answering, but its own plugin/health check " +
				"failed — read the per-plugin health_message in this report's plugin " +
				"inventory for what it said. A caller's tool and route calls to it may be " +
				"refused; see internal/pluginhost's health gate.",
		}
	}
	return Check{
		Name:   CheckPlugins,
		Status: StatusPass,
		Detail: strconv.Itoa(inventory.Loaded) + " plugin(s) loaded, " +
			strconv.Itoa(len(inventory.ContributedKinds)) + " contributed kind(s), " +
			strconv.Itoa(len(inventory.Tools)) + " tool(s), " +
			strconv.Itoa(len(inventory.Routes)) + " route(s)",
	}
}

// unhealthyPlugins names every loaded plugin whose cached health verdict is
// false, sorted for a stable report.
func unhealthyPlugins(inventory PluginInventory) []string {
	var names []string
	for _, record := range inventory.Plugins {
		if record.Loaded && record.Healthy != nil && !*record.Healthy {
			names = append(names, record.ID)
		}
	}
	sort.Strings(names)
	return names
}

// Plugins returns the inventory for a caller that wants the detail behind the
// check. It is bounded and empty-safe: a build with no probe answers with a
// zero inventory rather than nil, so a consumer never has to distinguish
// `null` from `[]` before it can read the document.
func (r *Reporter) Plugins(ctx context.Context) PluginInventory {
	if r.plugins == nil {
		return PluginInventory{Plugins: []PluginRecord{}}
	}
	inventory := r.plugins(ctx)
	if inventory.Plugins == nil {
		inventory.Plugins = []PluginRecord{}
	}
	for i := range inventory.Plugins {
		inventory.Plugins[i].Error = bound(inventory.Plugins[i].Error)
		inventory.Plugins[i].RuntimeError = bound(inventory.Plugins[i].RuntimeError)
		inventory.Plugins[i].HealthMessage = bound(inventory.Plugins[i].HealthMessage)
	}
	return inventory
}

// joinBounded renders a list of ids for a one-line detail, cut rather than
// wrapped. bound() would truncate mid-id; naming the overflow as a count keeps
// every id that IS printed readable.
func joinBounded(ids []string) string {
	const maxNamed = 4
	if len(ids) <= maxNamed {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:maxNamed], ", ") + " (+" + strconv.Itoa(len(ids)-maxNamed) + " more)"
}
