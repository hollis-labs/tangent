// Package plugins loads the plugins this Tangent has INSTALLED.
//
// # There is no compiled-in roster any more
//
// Until CW-20260911-0070 this package held `Shipped()` — a slice of Go
// constructors, one per plugin, linked into the server. That slice was the
// answer to "which plugins does this binary have", and it was answerable by
// reading one file precisely because a plugin was part of the binary.
//
// That is what the migration removes. An application plugin holds its own
// dependency in its own process, so Tangent's binary does not link it, does not
// version with it, and cannot be taken down by it. The cost of that is the
// thing the roster was buying: "which plugins does this binary have" is no
// longer a question about the binary. It is a question about the install
// directory, and `tangent plugin list` is how it is answered.
//
// # What is gained, stated as the property rather than the feature
//
// `internal/plugins/torque/torque.go` used to be the only file in the
// repository that knew Torque existed, and it was linked into the server. It is
// now a separate program in its own repository,
// github.com/hollis-labs/tangent-plugins (CW-20260930-0102). Tangent's binary
// is domain-free by construction rather than by a boundary somebody maintains.
package plugins

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hollis-labs/tangent/internal/authz"

	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/pluginintent"
	"github.com/hollis-labs/tangent/internal/pluginpkg"
	tangentplugin "github.com/hollis-labs/tangent/pkg/plugin"
)

// LoadInstalled discovers the plugins installed under root and loads them onto
// a new host.
//
// # A broken plugin is one plugin, not the boot
//
// Discovery already refuses a plugin per-plugin rather than aborting the scan
// (see internal/pluginpkg). This continues that: a plugin that fails to SPAWN
// is recorded and the others load.
//
// That is a deliberate change from the compiled-in behavior, which failed the
// whole boot on any plugin's Load error. Failing the boot was right when a
// plugin was part of the binary — a plugin that could not load meant a binary
// that should not have linked, and there was no operator action between build
// and run. An installed plugin is different: it can be stale, built for another
// host, or half-copied, and none of those should stop Tangent from serving
// every other surface it has. The refusals are reported through
// `tangent.health_report`, which is where "which plugins loaded, which refused
// and why" already lives.
func LoadInstalled(
	ctx context.Context,
	logger *slog.Logger,
	envSvc *envelope.Service,
	root string,
	// disableHealthGate turns off the CW-20260911-0069 health gate for every
	// plugin this call loads. Named for the opt-out, like boot.Config's field
	// it carries: false (the zero value, what a caller that omits it gets) is
	// the gate staying on.
	disableHealthGate bool,
	options ...LoadOption,
) (*pluginhost.Host, error) {
	host, err := pluginhost.New(ctx, logger, envSvc)
	if err != nil {
		return nil, err
	}

	settings := loadOptions{}
	for _, option := range options {
		option(&settings)
	}
	if settings.config != nil {
		if err = host.ConfigureConfig(settings.config); err != nil {
			return nil, err
		}
	}
	if root == "" {
		root, err = pluginpkg.DefaultRoot()
		if err != nil {
			return nil, fmt.Errorf("plugins: resolve install root: %w", err)
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("plugins: resolve install root: %w", err)
	}
	if intentErr := host.ConfigureIntent(pluginintent.Path(root)); intentErr != nil {
		return nil, fmt.Errorf("plugins: enabled intent: %w", intentErr)
	}
	installed, rejected, err := pluginpkg.Scan(root)
	if err != nil {
		return nil, fmt.Errorf("plugins: %w", err)
	}
	for _, reject := range rejected {
		// Recorded on the host so the health inventory carries it, and logged
		// so an operator watching a boot sees it go by. An install that cannot
		// run should be visible in both places: the log is what they are
		// looking at, the report is what they can ask for later.
		host.RecordUnusable(reject.Dir, reject.Reason)
		logger.Warn("plugins: installed plugin is not usable",
			"dir", reject.Dir, "reason", reject.Reason)
	}

	for _, source := range installed {
		id := source.Manifest.ID
		if settings.config != nil {
			if err := settings.config.Register(ctx, id, source.Manifest.Config); err != nil {
				host.RecordUnusable(id, err)
				continue
			}
		}
		factory := func() (sdkplugin.Plugin, error) {
			// Rescan the exact installed directory on every operation: install
			// metadata, integrity and entrypoint must be verified again.
			current, refused, scanErr := pluginpkg.Scan(root)
			if scanErr != nil {
				return nil, scanErr
			}
			for _, reject := range refused {
				if reject.Dir == source.Dir {
					return nil, reject.Reason
				}
			}
			for _, item := range current {
				if item.Manifest.ID != id {
					continue
				}
				if kindErr := resolveKinds(envSvc, item.Manifest.Bindings.Kinds); kindErr != nil {
					return nil, kindErr
				}
				if settings.config != nil {
					if err := settings.config.Register(ctx, id, item.Manifest.Config); err != nil {
						return nil, err
					}
				}
				entry, cleanup, snapshotErr := pluginpkg.Snapshot(item, root)
				if snapshotErr != nil {
					return nil, snapshotErr
				}
				spec := specFor(entry, root, cleanup)
				if settings.config != nil {
					spec.ResolveConfig = host.ResolveConfiguration(id)
					spec.ConfigApplied = func(ctx context.Context, revision string) error {
						return settings.config.MarkApplied(ctx, id, revision)
					}
				}
				return pluginhost.NewChildPlugin(spec, toolsFor(entry), routesFor(entry), pluginhost.WithHealthGate(!disableHealthGate)), nil
			}
			return nil, fmt.Errorf("plugins: %s is no longer installed", id)
		}
		if err := host.RegisterFactory(id, factory); err != nil {
			return nil, err
		}
		if !host.DesiredEnabled(id) {
			continue
		}
		if loadErr := host.SetEnabled(ctx, id, true); loadErr != nil {
			host.RecordUnusable(id, loadErr)
			logger.Warn("plugins: installed plugin failed to load", "plugin", id, "error", loadErr)
		}
	}
	return host, nil
}

// pluginEnvironment is what every child is told about its host.
//
// TANGENT_MCP_URL is how a plugin calls back — see specFor. It is derived from
// the same environment Tangent's own server reads for its port, so a Tangent on
// a non-default port hands its children the URL that actually reaches it rather
// than one that used to.
func pluginEnvironment() []string {
	return []string{"TANGENT_MCP_URL=" + mcpURL()}
}

// mcpURL is the loopback address of this host's MCP endpoint.
//
// 127.0.0.1 rather than "localhost" for the reason internal/server already logs
// it that way: on a system where localhost resolves to ::1 first without an
// IPv4 fallback, the name dead-ends against an IPv4-only listener.
func mcpURL() string {
	port := defaultHTTPPort
	if raw := os.Getenv(envHTTPPort); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			port = parsed
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

const (
	envHTTPPort     = "TANGENT_HTTP_PORT"
	defaultHTTPPort = 7842
)

// specFor builds the spawn spec for one installed plugin.
//
// # The MCP URL, and why it goes through the environment
//
// A subprocess plugin cannot call back into Tangent over the plugin wire: the
// SDK's protocol is host-initiated and says so — "the subprocess does not
// initiate requests" — and `subprocess/server.go` never constructs a request.
// Both application plugins need to call back; opening a board is
// `tangent.session_create` and syncing one is `tangent.surface_get` plus
// `tangent.session_advance`.
//
// So a child reaches Tangent the way any other local process does: as an MCP
// client against `/mcp`. That is not a workaround for a missing channel. It is
// the channel — `internal/pluginhost/tools.go` defines its in-process
// `ToolCaller` as granting "no authority a local MCP caller does not already
// have", so a child on `/mcp` does not approximate that equivalence, it IS it.
// And a subprocess plugin is a local process: it can reach `/mcp` whether or
// not this host intends it to. The only thing this decides is whether the
// intended path is the real one.
//
// The callback address stays in the environment. Reviewed configuration is
// resolved separately into each incarnation's Init.Config by the scoped host
// store; this address does not confer additional capability grants.

func specFor(entry pluginpkg.Installed, root string, cleanup func() error) pluginhost.ChildSpec {
	return pluginhost.ChildSpec{
		ID:       entry.Manifest.ID,
		Version:  entry.Manifest.Version,
		Command:  entry.Entrypoint,
		WorkDir:  entry.Dir,
		DataDir:  filepath.Join(root, ".state", entry.Manifest.ID, "data"),
		CacheDir: filepath.Join(root, ".state", entry.Manifest.ID, "cache"),
		Env:      pluginEnvironment(),
		Verify:   func() error { return entry.Manifest.VerifyBundle(entry.Dir) },
		Cleanup:  cleanup,
	}
}

// toolsFor projects the manifest's tool declarations into host registrations.
//
// The host holds the schema and advertises it; the child is never asked what it
// serves. That is ADR 0008 §3's host-manifest-authoritative ruling, and it is
// the reason `internal/pluginhost` deliberately never calls `mcp/list_tools`.
// Reading a declaration the host itself parsed at install time does not reverse
// it — what would reverse it is asking the running child.
func toolsFor(entry pluginpkg.Installed) []pluginhost.MCPTool {
	tools := make([]pluginhost.MCPTool, 0, len(entry.Manifest.Tools))
	for _, declared := range entry.Manifest.Tools {
		tools = append(tools, pluginhost.MCPTool{
			Name:        declared.Name,
			Description: declared.Description,
			InputSchema: declared.InputSchema,
			Effect:      declared.Effect,
			Annotations: declared.Annotations,
		})
	}
	return tools
}

// routesFor projects the manifest's route declarations.
//
// The capability is what the plugin says the route NEEDS. The host checks it
// against the participant's session and refuses one a participant can never
// hold, exactly as it does for a compiled-in plugin — declaring it here grants
// nothing.
func routesFor(entry pluginpkg.Installed) []pluginhost.HTTPRoute {
	routes := make([]pluginhost.HTTPRoute, 0, len(entry.Manifest.Bindings.Routes))
	for _, declared := range entry.Manifest.Bindings.Routes {
		routes = append(routes, pluginhost.HTTPRoute{
			Method:     declared.Method,
			Path:       declared.Path,
			Capability: authz.Capability(declared.Capability),
		})
	}
	return routes
}

// Only the host-approved definition tree can supply a kind. A process bundle
// cannot author trust or renderer bytes by naming it in the extension.
func resolveKinds(svc *envelope.Service, refs []tangentplugin.KindRef) error {
	for _, ref := range refs {
		found := false
		for _, item := range svc.MaterializedDefinitions() {
			if item.Manifest.Kind == ref.Kind {
				found = true
				break
			}
		}
		if !found {
			if err := extensions.RegisterContributedKind(svc, ref.Kind); err != nil {
				return fmt.Errorf("plugins: kind %s is not available: %w", ref.Kind, err)
			}
		}
		available := false
		for _, item := range svc.MaterializedDefinitions() {
			if item.Manifest.Kind == ref.Kind && item.Manifest.PackageID == ref.Package && item.Manifest.Version == ref.Version && item.State.Servable() {
				available = true
				break
			}
		}
		if !available {
			return fmt.Errorf("plugins: kind %s@%s from %s is not available", ref.Kind, ref.Version, ref.Package)
		}
	}
	return nil
}
