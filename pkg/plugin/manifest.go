package plugin

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A plugin.yaml is the host's record of what a plugin contributes, read once
// at install and at boot — not the plugin's runtime self-declaration, which
// ADR 0008 §3 rules out. A plugin emits it (conventionally from a --manifest
// flag) and the host validates, installs and discovers it
// (internal/pluginpkg). It is not ADR 0003's interaction definition manifest:
// it says what a PROCESS is and how to start it, and grants nothing.

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
	// The host refuses any version but its own; see
	// internal/pluginpkg.CheckCompatible for why.
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

// Validate reports whether a manifest is usable, without reference to a
// filesystem. It is what both install and discovery run.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("plugin: %s declares no id", ManifestName)
	}
	if strings.TrimSpace(m.Entrypoint) == "" {
		return fmt.Errorf("plugin: %s declares no entrypoint", m.ID)
	}
	// An entrypoint is a name inside the plugin's own directory. Anything that
	// escapes it — absolute, or containing a parent reference — would make
	// installing a plugin a way to run something already on the system, which
	// is a different and much larger permission than "run what I installed".
	if filepath.IsAbs(m.Entrypoint) {
		return fmt.Errorf(
			"plugin: %s entrypoint %q is absolute; it must name a file inside the "+
				"plugin's own directory", m.ID, m.Entrypoint)
	}
	clean := filepath.Clean(m.Entrypoint)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"plugin: %s entrypoint %q escapes the plugin directory", m.ID, m.Entrypoint)
	}
	if m.Protocol <= 0 {
		return fmt.Errorf(
			"plugin: %s declares no protocol version; the host refuses a plugin whose "+
				"wire version it cannot check", m.ID)
	}
	for i, tool := range m.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("plugin: %s tools[%d] has no name", m.ID, i)
		}
		if strings.TrimSpace(tool.InputSchema) == "" {
			return fmt.Errorf(
				"plugin: %s tool %q has no input_schema; the host advertises it, so the "+
					"host has to hold it", m.ID, tool.Name)
		}
	}
	for i, route := range m.Routes {
		if strings.TrimSpace(route.Path) == "" {
			return fmt.Errorf("plugin: %s routes[%d] has no path", m.ID, i)
		}
		if strings.TrimSpace(route.Capability) == "" {
			return fmt.Errorf(
				"plugin: %s route %q names no capability", m.ID, route.Path)
		}
	}
	return nil
}
