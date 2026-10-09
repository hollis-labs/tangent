package ledger

import (
	"io"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	tangentplugin "github.com/hollis-labs/tangent/pkg/plugin"
)

// WriteManifest emits plugin.yaml for a fresh bundle containing the final native
// executable at bin/tangent-plugin-ledger. This declaration grants nothing.
// The program's --manifest path reads its own executable and calls this method.
func (p *Plugin) WriteManifest(w io.Writer, payload []byte) error {
	common := sdkmanifest.Manifest{
		ID: p.ID(), Name: p.Name(), Description: p.Description(), Version: p.Version(),
		Tools: []sdkmanifest.Tool{
			{Name: OpenTool, Description: openToolDescription, InputSchema: openToolSchema, Effect: "write"},
			{Name: SyncTool, Description: syncToolDescription, InputSchema: syncToolSchema, Effect: "write"},
		},
		Config: sdkmanifest.Config{
			Fields: map[string]sdkmanifest.Field{"api_url": {Type: "string", Env: "TANGENT_LEDGER_API_URL"}},
		},
	}
	bindings := tangentplugin.TangentExtension{
		SchemaVersion: tangentplugin.TangentSchemaVersion,
		Kinds: []tangentplugin.KindRef{
			{Kind: EnvelopeType, Package: "tangent.appboard", Version: "0.3"},
		},
		Routes: []tangentplugin.RouteDecl{
			{Method: "POST", Path: SyncPath, Capability: "draft"},
		},
		MCPTools: []string{OpenTool, SyncTool},
	}
	return tangentplugin.EncodeNativeManifest(w, payload, "bin/tangent-plugin-ledger", common, bindings)
}
