package plugins

import (
	"github.com/hollis-labs/tangent/internal/childkeys"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

type loadOptions struct {
	config *pluginconfig.Store
	// callback is how a child reaches the host: the /mcp URL and, when set, the leases that
	// give each child its own key.
	mcpURL string
	keys   *childkeys.Leases
}
type LoadOption func(*loadOptions)

// WithConfigStore supplies a composition-owned scoped configuration service.
func WithConfigStore(store *pluginconfig.Store) LoadOption {
	return func(options *loadOptions) { options.config = store }
}

// WithCallback tells every child where the host's /mcp is (the address the host actually
// listens on) and, with keys, gives each child its own short-lived identity key, delivered as the
// path of a file in TANGENT_MCP_KEY_FILE. Without keys a child has no credential, and /mcp
// refuses it.
func WithCallback(mcpURL string, keys *childkeys.Leases) LoadOption {
	return func(options *loadOptions) { options.mcpURL, options.keys = mcpURL, keys }
}
