// Package plugins is the list of compiled-in plugins this build ships, and the
// one function that loads them.
//
// It exists so the server and the codegen dump tool cannot describe different
// registries. `cmd/tangent-dump-types` already documents that reason for going
// through `extensions.RegisterAll` rather than assembling its own registry; a
// kind that arrives through the ADR 0007 §4 plugin door needs the same
// treatment, or the generated TypeScript would be missing exactly the kinds the
// server serves through the newer path.
package plugins

import (
	"context"
	"fmt"
	"log/slog"

	plugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/plugins/appboard"
	"github.com/hollis-labs/tangent/internal/plugins/tesseract"
	"github.com/hollis-labs/tangent/internal/plugins/torqueboard"
)

// Shipped returns the compiled-in plugins in load order.
//
// Compiled-in is the only mode (ADR 0007 §4). There is no discovery here, no
// directory scan and no subprocess spawn: what a build ships is what is in this
// slice, which is what makes "which plugins does this binary have" answerable
// by reading one file.
func Shipped() []plugin.Plugin {
	return []plugin.Plugin{
		// Order is load order, and it is a dependency order: both application
		// plugins declare appboard as a dependency because they supply content
		// to the kind appboard contributes, and the host refuses a plugin whose
		// stated dependency is not already loaded.
		appboard.New(),
		torqueboard.New(),
		tesseract.New(),
	}
}

// LoadShipped builds the plugin host and loads every shipped plugin onto it.
// Callers pass the envelope service that already carries the host-package kinds
// from extensions.RegisterAll; the plugins register theirs on top.
//
// A plugin that fails to load fails the call. There is no partial mode: a kind
// that did not register is a kind the UI has a renderer for and the server will
// refuse a submission against, and discovering that at boot is the point.
func LoadShipped(
	ctx context.Context,
	logger *slog.Logger,
	envSvc *envelope.Service,
) (*pluginhost.Host, error) {
	host, err := pluginhost.New(ctx, logger, envSvc)
	if err != nil {
		return nil, err
	}
	for _, p := range Shipped() {
		if loadErr := host.Load(p); loadErr != nil {
			return nil, fmt.Errorf("plugins: %w", loadErr)
		}
	}
	return host, nil
}
