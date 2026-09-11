// Package appboard is the compiled-in plugin that contributes the
// `tangent.app-board` interaction kind.
//
// It is the first consumer of internal/pluginhost, and it is deliberately
// almost empty. That is the point of the ADR 0007 §4 boundary: a plugin names a
// kind, and the host decides what that kind may do by resolving the manifest it
// ships. There is nothing here to review for a trust decision, because there is
// nothing here a trust decision could be made from.
//
// It lives in-tree (Chrispian's decision, 2026-09-09). ADR 0007 §4 already says
// where a plugin's code lives is not the boundary: in-tree and out-of-tree
// plugins are subject to identical manifest and trust rules, and this location
// is convenience, not permission. Moving it to
// plugins/tangent-plugin-appboard/ later changes its import path and nothing
// about what it is allowed to do.
package appboard

import (
	"fmt"
	"time"

	plugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
)

// ID is the plugin identifier, and the name that appears in host logs.
const ID = "tangent.plugin.appboard"

// Plugin contributes the app-board envelope kind. It holds no state beyond its
// own load status: the kind's definition lives in its manifest, its renderer is
// compiled into ui_dist, and its MCP tool is registered by internal/mcp.
type Plugin struct {
	status plugin.PluginStatus
}

// New returns an unloaded app-board plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) ID() string      { return ID }
func (p *Plugin) Name() string    { return "App board" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Description() string {
	return "Contributes the tangent.app-board interaction kind: a domain-free board of " +
		"caller-supplied cards in columns, with a filter bar and an optional detail pane."
}

// Dependencies returns none. The kind depends on the host's manifest tree,
// which is not a plugin.
func (p *Plugin) Dependencies() []string { return nil }

// Load registers the envelope kind with the host.
//
// The component carries a name and a type and nothing else. It sets no Props:
// the trust class, the granted capabilities, the assurance and the digests are
// the host's to decide from the manifest, and the host refuses a component that
// tries to author any of them. Leaving Props nil is not an oversight — it is
// the plugin having nothing to say about those things.
func (p *Plugin) Load(host plugin.Host) error {
	if host == nil {
		return fmt.Errorf("appboard: host is nil")
	}
	if err := host.RegisterUIComponent(plugin.UIComponent{
		// ID is the kind's wire name. It is what the host resolves a manifest
		// for, and a name with no manifest is refused.
		ID:          extensions.AppBoardEnvelopeType,
		Type:        plugin.UIComponentTypeEnvelope,
		Name:        "AppBoardView",
		Description: "Board of caller-supplied cards with filters and a detail pane.",
	}); err != nil {
		p.status.LastError = err.Error()
		return err
	}
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	return nil
}

// Unload drops the plugin's own status. It does NOT unregister the kind, and
// that is the host's contract rather than this plugin's choice: see
// internal/pluginhost/lifecycle.go. go-envelopes' registry is boot-time and has
// no removal, and a definition that vanished from a running registry would
// leave live interactions referring to a kind the host could no longer explain.
// A plugin that should not be present is one that is not loaded at boot.
func (p *Plugin) Unload() error {
	p.status = plugin.PluginStatus{}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus { return p.status }

// Compile-time proof this satisfies the SDK contract.
var _ plugin.Plugin = (*Plugin)(nil)
