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
// its own React tree, contributed through the ADR 0007 §4 plugin path.
const AppBoardEnvelopeType = "tangent.app-board"

// AppBoardPackageID is the package this kind ships in. It is its own package
// rather than a tenant of tangent.canvas so that the plugin-contributed path is
// visible in the tree rather than inferable only from register_all.go.
const AppBoardPackageID = "tangent.appboard"

// RegisterAppBoard registers the app-board definition from its shipped package.
// The manifest at packages/tangent.appboard/app-board/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Production does NOT reach this through RegisterAll. It is the one shipped
// kind that arrives through the plugin host: internal/plugins/appboard declares
// an envelope UI component, internal/pluginhost resolves this manifest and
// refuses the registration if it cannot, and RegisterContributedKind is what
// finally calls this. RegisterAll skips it for exactly that reason — a kind
// registered twice is an error, and go-envelopes is right to say so.
//
// It stays exported on the same terms as every other Register* function: for
// tests that deliberately boot a partial registry.
func RegisterAppBoard(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, AppBoardPackageID, AppBoardEnvelopeType)
}
