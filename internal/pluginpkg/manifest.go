// Package pluginpkg is the installed-plugin format: what a `plugin.yaml`
// declares, where installed plugins live, and how the host finds them
// (CW-20260911-0070).
//
// # Whose declaration this is
//
// The host's. That distinction is load-bearing and it is the same ruling
// `internal/pluginhost` already applies: Tangent deliberately never calls
// `mcp/list_tools`, because Nanite built runtime self-declaration and discarded
// it, and ADR 0008 §3 records the host-manifest-authoritative result.
//
// A `plugin.yaml` does not reverse that. It is read ONCE, by the host, at
// install and at boot — before the child is running and before it could answer
// for itself. What the host reads there becomes the host's own record of what
// that plugin contributes, and the child is told what it was registered for at
// dispatch time. A child never gets to widen its own surface between two boots
// of the same host, which is exactly what runtime self-declaration allowed.
//
// If that ever needs to change, it is a reversal of ADR 0008 §3 and wants its
// own record — not a field quietly added here.
//
// # Why this is not ADR 0003's manifest
//
// `internal/definition` owns interaction definition manifests: what a KIND is,
// what may render it, what it may reach. Those are versioned, digested and
// frozen, and ADR 0003 §6 assigns each one an owning package.
//
// This is a different document with a different job — what a PROCESS is, and
// how to start it. It carries no trust claim, grants nothing, and is not part
// of any digest. A plugin that contributes a kind still ships that kind's ADR
// 0003 manifest through the host's own package tree, and this file cannot
// substitute for it.
package pluginpkg

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/pkg/plugin"
)

// ManifestName, Manifest, ToolDecl and RouteDecl are defined in pkg/plugin,
// because a plugin emits its own plugin.yaml, and aliased here. What the host
// does with one — CheckCompatible, ParseManifest, discovery and install — stays
// in this package.

// ManifestName is the file an installed plugin is recognized by. See
// plugin.ManifestName.
const ManifestName = plugin.ManifestName

// Manifest is an installed plugin's declaration. See plugin.Manifest.
type Manifest = plugin.Manifest

// ToolDecl is one MCP tool an installed plugin serves. See plugin.ToolDecl.
type ToolDecl = plugin.ToolDecl

// RouteDecl is one browser route an installed plugin serves. See
// plugin.RouteDecl.
type RouteDecl = plugin.RouteDecl

// ErrIncompatible reports a plugin built against a wire version this host does
// not speak.
var ErrIncompatible = fmt.Errorf("pluginpkg: plugin protocol is not this host's")

// CheckCompatible decides whether this host will run a plugin.
//
// # The policy, and why it refuses rather than negotiates
//
// The wire version must match EXACTLY. Not a range, not a floor, not a
// best-effort downgrade.
//
// A range would be a promise that this host understands every version inside
// it, which nothing checks and nothing could — the protocol carries no
// capability negotiation, so there is no way for two ends to discover what they
// disagree about. A floor is the same promise with one bound removed. Both fail
// the same way: a plugin loads, then behaves subtly differently at one method,
// and the failure surfaces as a wrong answer rather than a refusal.
//
// A permissive fallback here is the defect class ADR 0003 §2.7 forbids in its
// own domain: `unverified` is a reserved value with no producer precisely so it
// cannot become the answer when nothing else fits. The same discipline applies
// to a protocol version. A mismatch means one of the two needs rebuilding, and
// saying so at boot costs an operator one line; discovering it at the first
// tool call costs them a debugging session.
//
// This is cheap to hold because plugin and host are both first-party today. It
// gets expensive exactly when third-party plugins arrive, which is out of scope
// (ADR 0008 §4) and would want a negotiation story rather than a wider range.
func CheckCompatible(m Manifest) error {
	if m.Protocol != subprocess.ProtocolVersion {
		return fmt.Errorf(
			"%w: %s was built against wire protocol %d and this host speaks %d. "+
				"The wire carries no capability negotiation, so a near-miss cannot be "+
				"detected at the method that differs — rebuild the plugin, or run a host "+
				"that matches it",
			ErrIncompatible, m.ID, m.Protocol, subprocess.ProtocolVersion)
	}
	return nil
}

// ParseManifest reads and validates one plugin.yaml.
func ParseManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path comes from a directory walk of the install root.
	if err != nil {
		return Manifest{}, fmt.Errorf("pluginpkg: read %s: %w", path, err)
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("pluginpkg: parse %s: %w", path, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return manifest, nil
}
