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
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// ManifestName is the file an installed plugin is recognized by. A directory
// without one is not a plugin, which is how a stray directory under the install
// root is ignored rather than reported as broken.
const ManifestName = "plugin.yaml"

// Manifest is an installed plugin's declaration.
type Manifest struct {
	// ID is the plugin identity the host keys on, and it must match the id the
	// child returns from `plugin/init`. A mismatch is refused: a plugin
	// installed as one thing and answering as another is the ambiguity the
	// host's roster exists to prevent.
	ID string `yaml:"id"`
	// Name and Description are for operators and health reports.
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Version is the plugin's own version, independent of Tangent's. That
	// independence is the point of installing rather than compiling in.
	Version string `yaml:"version"`

	// Protocol is the subprocess wire version this plugin was built against.
	// See CheckCompatible for what a mismatch does and why.
	Protocol int `yaml:"protocol"`

	// Entrypoint is the executable, relative to the plugin's own directory.
	// It is deliberately not an absolute path and not a shell string: a
	// manifest that could name any binary on the system would make installing
	// a plugin equivalent to granting arbitrary execution, and an install
	// directory is not a place a reviewer looks.
	Entrypoint string `yaml:"entrypoint"`
	// Args are passed to the entrypoint.
	Args []string `yaml:"args,omitempty"`

	// Tools are the MCP tools this plugin serves. The host registers them and
	// dispatches to the child by name; the child is never asked what it has.
	Tools []ToolDecl `yaml:"tools,omitempty"`
	// Routes are the browser routes this plugin serves.
	Routes []RouteDecl `yaml:"routes,omitempty"`
}

// ToolDecl is one MCP tool an installed plugin serves.
type ToolDecl struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// InputSchema is the tool's JSON Schema, inline. It is the host that
	// advertises this to callers, so it is the host that has to hold it.
	InputSchema string `yaml:"input_schema"`
}

// RouteDecl is one browser route an installed plugin serves.
type RouteDecl struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	// Capability is the ADR 0004 §2 capability the route exercises. The host
	// checks it against the participant's session before dispatching, and
	// refuses a capability a participant can never hold — a plugin declaring
	// one here is naming what it needs, not granting itself anything.
	Capability string `yaml:"capability"`
}

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
func (m Manifest) CheckCompatible() error {
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

// Validate reports whether a manifest is usable, without reference to a
// filesystem. It is what both install and discovery run.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("pluginpkg: %s declares no id", ManifestName)
	}
	if strings.TrimSpace(m.Entrypoint) == "" {
		return fmt.Errorf("pluginpkg: %s declares no entrypoint", m.ID)
	}
	// An entrypoint is a name inside the plugin's own directory. Anything that
	// escapes it — absolute, or containing a parent reference — would make
	// installing a plugin a way to run something already on the system, which
	// is a different and much larger permission than "run what I installed".
	if filepath.IsAbs(m.Entrypoint) {
		return fmt.Errorf(
			"pluginpkg: %s entrypoint %q is absolute; it must name a file inside the "+
				"plugin's own directory", m.ID, m.Entrypoint)
	}
	clean := filepath.Clean(m.Entrypoint)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"pluginpkg: %s entrypoint %q escapes the plugin directory", m.ID, m.Entrypoint)
	}
	if m.Protocol <= 0 {
		return fmt.Errorf(
			"pluginpkg: %s declares no protocol version; the host refuses a plugin whose "+
				"wire version it cannot check", m.ID)
	}
	for i, tool := range m.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("pluginpkg: %s tools[%d] has no name", m.ID, i)
		}
		if strings.TrimSpace(tool.InputSchema) == "" {
			return fmt.Errorf(
				"pluginpkg: %s tool %q has no input_schema; the host advertises it, so the "+
					"host has to hold it", m.ID, tool.Name)
		}
	}
	for i, route := range m.Routes {
		if strings.TrimSpace(route.Path) == "" {
			return fmt.Errorf("pluginpkg: %s routes[%d] has no path", m.ID, i)
		}
		if strings.TrimSpace(route.Capability) == "" {
			return fmt.Errorf(
				"pluginpkg: %s route %q names no capability", m.ID, route.Path)
		}
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
