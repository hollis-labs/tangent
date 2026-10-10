package plugins

import "github.com/hollis-labs/tangent/internal/pluginconfig"

type loadOptions struct{ config *pluginconfig.Store }
type LoadOption func(*loadOptions)

// WithConfigStore supplies a composition-owned scoped configuration service.
func WithConfigStore(store *pluginconfig.Store) LoadOption {
	return func(options *loadOptions) { options.config = store }
}
