package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"
	substratemcp "github.com/hollis-labs/substrate/modules/mcp"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/boot"
	"github.com/hollis-labs/tangent/internal/childkeys"
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
	"github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/settings"
)

// Environment the app still reads. Everything about where it listens, which
// database it opens and who may call it moved to the manifest's config and to
// identity; what is left here is behavior switches and the optional d2 binary.
const (
	envArtifactDir             = "TANGENT_ARTIFACT_DIR"
	envD2Bin                   = "TANGENT_D2_BIN"
	envManagedResource         = "TANGENT_MANAGED_RESOURCE"
	envOpenTelemetry           = "TANGENT_OTEL"
	envDisablePluginHealthGate = "TANGENT_PLUGIN_HEALTH_GATE_DISABLE"
	envListLegacyToolNames     = "TANGENT_MCP_LEGACY_TOOL_NAMES"
	envEventsDisabled          = "TANGENT_EVENTS_DISABLED"
	envTetherSendOnly          = "TANGENT_TETHER_TOKEN"
)

// activeTools is the live MCP server, for the tools/list filter that must be
// registered before the modules start.
var activeTools atomic.Pointer[mcp.Server]

func init() { substratemcp.UseToolFilter(visibleTools) }

// visibleTools lists to a caller only the tools it may call. It is visibility,
// not authorization: the call is authorized on its own.
func visibleTools(ctx context.Context, names []string) []string {
	srv := activeTools.Load()
	if srv == nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if scope, ok := srv.ToolScope(name); ok && access.AuthorizeTool(ctx, name, scope) == nil {
			out = append(out, name)
		}
	}
	return out
}

// compose builds Tangent's service graph over the started modules' database,
// MCP server and identity.
func (t *tangent) compose(cfg settings.Settings) (*boot.Services, *server.Table, io.Closer, error) {
	app := t.app
	getenv := t.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	st := database.From(app)
	id := identity.From(app)
	tools := substratemcp.Server(app)
	if st == nil || id == nil || tools == nil {
		return nil, nil, nil, errors.New("the database, identity and mcp modules must be started")
	}
	dirs, err := appdirs.Resolve(getenv)
	if err != nil {
		return nil, nil, nil, err
	}
	artifactDir := getenv(envArtifactDir)
	if artifactDir == "" {
		artifactDir = dirs.Artifacts()
	}
	dbPath := ""
	if db, _ := app.Config["database"].(map[string]any); db != nil {
		if sq, _ := db["sqlite"].(map[string]any); sq != nil {
			dbPath, _ = sq["path"].(string)
		}
	}
	issuer, err := newKeyIssuer(app, id)
	if err != nil {
		return nil, nil, nil, err
	}
	// Each plugin child gets its own key, a few minutes long and renewed while it lives.
	childKeys, err := childkeys.New(issuer, access.PluginChildScopes(), childKeyLifetime, childKeyRenewal, slog.Default())
	if err != nil {
		return nil, nil, nil, err
	}
	services, table, closer, err := boot.Boot(boot.Config{
		DB:                      st.DB(),
		DBPath:                  dbPath,
		ArtifactDir:             artifactDir,
		D2Bin:                   getenv(envD2Bin),
		MCPServer:               tools,
		Authorizer:              access.AuthorizeTool,
		Callers:                 mcp.Callers{Gateway: cfg.GatewayProducers, Forwarders: cfg.TrustedForwarders},
		Verifier:                id.Verify,
		HITLBackend:             getenv(gohitl.EnvBackend),
		RoomURLBase:             "http://" + httpAddr(app),
		MCPURL:                  "http://" + httpAddr(app) + "/mcp",
		ChildKeys:               childKeys,
		ManagedResource:         getenv(envManagedResource),
		OTel:                    getenv(envOpenTelemetry) != "",
		DisablePluginHealthGate: getenv(envDisablePluginHealthGate) != "",
		ListLegacyToolNames:     getenv(envListLegacyToolNames) != "",
		EventsDisabled:          getenv(envEventsDisabled) == "1",
		ReplyToOptional:         cfg.ReplyToOptional,
		ReplyMailURL:            cfg.TetherURL,
		ReplyMailFrom:           cfg.TetherSender,
		// The send-only token moves to the secrets module with slice (d); until
		// then the host's environment supplies it, never the config file.
		ReplyMailToken: getenv(envTetherSendOnly),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	activeTools.Store(services.MCP)
	return services, table, closer, nil
}

// A plugin child's key lives ten minutes and is replaced every three while the child runs.
const (
	childKeyLifetime = 10 * time.Minute
	childKeyRenewal  = 3 * time.Minute
)

// httpAddr is the address the host listens on, as configured.
func httpAddr(app *module.App) string {
	cfg, _ := app.Config["httphost"].(map[string]any)
	if addr, _ := cfg["addr"].(string); addr != "" {
		return addr
	}
	return "127.0.0.1:8080"
}

// toolScope is the scope a tool needs on the running server (tests).
func (t *tangent) toolScope(name string) (string, bool) {
	srv := activeTools.Load()
	if srv == nil {
		return "", false
	}
	return srv.ToolScope(name)
}
