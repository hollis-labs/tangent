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
	"github.com/hollis-labs/tangent/internal/plugins/tesseract"
	"github.com/hollis-labs/tangent/internal/plugins/torque"
)

// Shipped returns the compiled-in plugins in load order.
//
// Compiled-in is the only mode (ADR 0007 §4). There is no discovery here, no
// directory scan and no subprocess spawn: what a build ships is what is in this
// slice, which is what makes "which plugins does this binary have" answerable
// by reading one file.
//
// # This slice IS the enable set, and that is a recorded decision
//
// CW-20260910-0036 asked whether plugins need an enable/disable flag, and the
// answer for this build is no — deferred with the reason, so its absence is not
// read later as an oversight.
//
// Nothing here has a caller for a runtime toggle: a compiled-in plugin is
// enabled by being in this slice and disabled by not being, and changing that
// is a rebuild, which is also what changing its code is. Tether's catalog has
// the flag and a documented failure mode that came with it — an entry marked
// enabled but unreachable stalls its proxy for 120 seconds — and the way not to
// inherit that trap is not to build the flag until something needs it, which is
// the same test ADR 0007 §4 applies to every host surface the SDK offers.
//
// What would change the answer is a plugin whose absence must be survivable at
// runtime rather than at build time. Subprocess mode (CW-20260910-0034) is the
// first candidate, because a spawned plugin can be unreachable in ways a
// compiled-in one cannot.
func Shipped() []plugin.Plugin {
	return []plugin.Plugin{
		// Order is load order. Neither of these declares a dependency: the kind
		// they both supply content to is `tangent.app-board`, which RegisterAll
		// installs before any plugin loads (CW-20260911-0036). A plugin depends
		// on another plugin or on nothing; it does not declare a dependency on
		// the host.
		torque.New(),
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
