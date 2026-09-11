package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// AppBoardEnvelopeType is the canonical wire name for the app-board kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// App board envelope: a domain-free board of agent-supplied cards in columns,
// with a filter bar and an optional detail pane.
//
// `app-board`, deliberately not `torque-board`. The kind describes a shape, not
// a domain: Torque is the first application to supply content to it and is not
// a type this kind knows about. ADR 0007 §6 is what that buys — the same kind
// serves the next application without Tangent learning anything about either.
//
// ADR 0003 §6 assigns it to its own package: presentation Tangent hosts inside
// its own React tree, and it is host plumbing — the board shape itself, which
// consumers fill and none of them owns.
const AppBoardEnvelopeType = "tangent.app-board"

// AppBoardPackageID is the package this kind ships in. It is its own package
// rather than a tenant of tangent.canvas because it is the one shape every
// application plugin supplies content to, and a package boundary is how ADR
// 0003 §6 says that out loud.
const AppBoardPackageID = "tangent.appboard"

// RegisterAppBoard registers the app-board definition from its shipped package.
// The manifest at packages/tangent.appboard/app-board/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// # It went through the plugin door for a while, and that was wrong
//
// From the day the plugin host landed until CW-20260911-0036, this kind was
// marked contributedByPlugin and installed by a plugin that did nothing else.
// The test it failed is Chrispian's own: helper and opinionated behavior are
// first-party plugins over HOST-OWNED PLUMBING. This is the plumbing — a
// domain-free board shape, published by `tangent`, `ownership_class:
// host-package`, rendered from a component compiled into `ui_dist` with the
// release, versioned with the repository. The plugin holding it had no
// dependency to isolate, no independent distribution and no domain knowledge to
// keep out of core, which is every property that would have made it a plugin.
//
// It was never *moved* to a plugin, which is why it read as load-bearing: the
// kind and the plugin were born in the same commit (7b4803e), so the plugin
// existed to give the new host a customer, and the two consumers that made the
// shape look shared arrived afterwards.
//
// RegisterAll installs it now, like every other host-package kind.
func RegisterAppBoard(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, AppBoardPackageID, AppBoardEnvelopeType)
}
