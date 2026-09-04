// Package effect is Tangent's host-mediated effect model: the capability
// namespace a renderer declares, the scoped handles that replace path strings,
// the single broker every effect passes through, and the typed receipt each
// one produces.
//
// # The two capability namespaces, and why they stay two
//
// Tangent has two capability namespaces that nearly collide. ADR 0003 §2.5 and
// ADR 0004 §2 deliberately left them separate so CW-20260825-0077 would
// reconcile them consciously. This package is that reconciliation, and the
// answer is: **two namespaces, two Go types, one conjunctive gate.**
//
//	authz.Capability   — object access.  view, submit, draft, resolve, cancel,
//	                     close, administer.  Answers "may this principal
//	                     perform this operation on this Tangent object?"
//	effect.Capability  — host-mediated effect.  file.read_scoped,
//	                     clipboard.write, export.download, network.fetch,
//	                     process.exec.  Answers "may this definition's
//	                     renderer cause the host to act on the world?"
//
// They are orthogonal axes of one decision, not two points on one scale, and
// merging them would produce exactly the confusing enum ADR 0003 §2.5 warned
// about. A participant's `resolve` says nothing about whether a renderer may
// read a file; a granted `file.read_scoped` says nothing about whether this
// browser may answer this interaction. The two are separate Go types in
// separate packages so the compiler refuses the substitution a merged enum
// would have permitted, and TestTheTwoCapabilityNamespacesAreDisjoint holds the
// identifier sets apart by name as well as by type.
//
// The single place they meet is [ObjectPrecondition]. Every effect names the
// object-access capability a principal must *already* hold before the effect
// is even considered, and [Broker.Request] evaluates the conjunction:
//
//	object access (authz.Authorize)
//	  AND the definition declared the effect  (ADR 0003 required_capabilities)
//	  AND host policy grants it               (ADR 0003 granted_capabilities)
//	  AND a host-minted handle scopes it      (this package)
//	  AND the participant expressed intent    (this package)
//
// Denying any one denies the effect. Neither namespace is the authority on its
// own, which is the failure mode the ADRs named: two systems that each think
// they are.
//
// # There was a third namespace
//
// `hitl.ArtifactPreviewCapability{Authority, CapabilityID}`
// (internal/hitl/evidence.go) is an ad-hoc third capability namespace that
// neither ADR names. Its *intent* was already right — its own doc comment says
// Tangent "never treats authority, artifact IDs, paths, URIs, or action IDs in
// an evidence payload as permission to read or execute anything" — but it is a
// separate registry with separate spelling. It is an instance of this
// namespace, and [EvidencePreview] is the capability id it maps onto. See
// docs/host-mediated-capabilities.md for the migration.
//
// # What is enforced and what is only declared
//
// Not every effect class can be made real in a browser that runs the renderer
// same-origin with the host. [Mediation] records which is which, per
// capability, in the type system rather than in a comment nobody reads. A
// declaration-only capability is audited and never described as enforced.
package effect

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/hollis-labs/tangent/internal/authz"
)

// Capability is one host-mediated effect a renderer cannot perform on its own
// authority (ADR 0003 §2.5).
//
// It is deliberately not [authz.Capability]. See the package doc.
type Capability string

const (
	// FileReadScoped reads bytes from a path inside a host-registered
	// workspace root, addressed by handle and never by a path string.
	FileReadScoped Capability = "file.read_scoped"

	// FileWriteScoped writes bytes to a path inside a host-registered
	// workspace root that the authority marked writable.
	FileWriteScoped Capability = "file.write_scoped"

	// EvidencePreview reads the content behind a durable evidence reference
	// through an explicitly registered host adapter. It is the capability id
	// `hitl.ArtifactPreviewCapability` was expressing without a namespace.
	EvidencePreview Capability = "evidence.preview"

	// ExportDownload hands the participant a file the host generated.
	ExportDownload Capability = "export.download"

	// ClipboardWrite places host-supplied text on the participant's clipboard.
	ClipboardWrite Capability = "clipboard.write"

	// NetworkFetch retrieves an external origin's bytes on the renderer's
	// behalf.
	NetworkFetch Capability = "network.fetch"

	// ProcessExec runs a program. Reserved, and refused by two independent
	// rules: its object precondition is `administer`, which nothing in the
	// shipped binary holds, and its mediation is [MediationUnimplemented],
	// which has no executor to reach.
	ProcessExec Capability = "process.exec"
)

// Mediation records how much of a capability the host can actually enforce.
//
// This distinction is the honest core of the model, so it is a type rather
// than a docstring: a reader of [Broker.Request] can see, per capability,
// whether the grant check is a barrier or a record.
type Mediation string

const (
	// MediationHost means the host is the only possible actor. A renderer has
	// no way to cause the effect itself, so refusing the request refuses the
	// effect. These are the capabilities that are genuinely enforced.
	MediationHost Mediation = "host"

	// MediationDeclared means the browser hands a same-origin renderer the
	// same power directly — `navigator.clipboard`, an `<a download>` over a
	// Blob, a bare `fetch()`. Routing it through the host produces a
	// declaration, an audit trail, and a scoped path for renderers that
	// cooperate; it is not a barrier against one that does not.
	//
	// It becomes MediationHost for a renderer that CW-20260825-0073 places in
	// a sandboxed trust class with a CSP, because then the browser stops
	// handing it the power. Until that lands, nothing here may be described
	// as enforced.
	MediationDeclared Mediation = "declared"

	// MediationUnimplemented means this build ships no executor at all. The
	// capability is reserved so the manifest format does not have to change
	// when one exists; every request is refused with `effect_unavailable`.
	MediationUnimplemented Mediation = "unimplemented"
)

// catalog is the whole namespace: every known effect capability, the
// object-access capability its holder must already have, and how much of it
// this build can enforce.
//
// A capability absent from this table is unknown, and an unknown capability is
// never a grant — the same rule [authz.ParseCapabilities] applies on the other
// axis.
var catalog = map[Capability]struct {
	precondition authz.Capability
	mediation    Mediation
}{
	// Reading content the participant is already entitled to see needs `view`
	// on the object and nothing more; the *scoping* is the handle's job.
	FileReadScoped:  {authz.View, MediationHost},
	EvidencePreview: {authz.View, MediationHost},

	// Writing on the participant's behalf is at least draft-level authorship,
	// so it takes `draft`. It is deliberately not `resolve`: writing a file is
	// not terminalizing an interaction, and conflating the two is exactly the
	// merged-namespace mistake.
	FileWriteScoped: {authz.Draft, MediationHost},

	// The browser gives a same-origin renderer all three of these directly.
	ExportDownload: {authz.View, MediationDeclared},
	ClipboardWrite: {authz.View, MediationDeclared},
	NetworkFetch:   {authz.View, MediationDeclared},

	// Nothing holds `administer` in the shipped binary (ADR 0004 §7), and
	// there is no executor. Both facts are load-bearing; neither is a
	// fallback for the other.
	ProcessExec: {authz.Administer, MediationUnimplemented},
}

// Known reports whether this build recognizes a capability id.
func Known(capability Capability) bool {
	_, ok := catalog[capability]
	return ok
}

// ObjectPrecondition returns the [authz.Capability] a principal must already
// hold on the object before this effect is considered.
//
// It is the one bridge between the two namespaces, and it is a mapping rather
// than an equivalence: it says which object-access question must be answered
// first, never that the two capabilities are the same thing. An unknown effect
// maps to [authz.Administer], so a capability this build does not know can
// never be reached by a principal that exists.
func ObjectPrecondition(capability Capability) authz.Capability {
	entry, ok := catalog[capability]
	if !ok {
		return authz.Administer
	}
	return entry.precondition
}

// MediationOf reports how much of a capability this build enforces. An unknown
// capability is [MediationUnimplemented].
func MediationOf(capability Capability) Mediation {
	entry, ok := catalog[capability]
	if !ok {
		return MediationUnimplemented
	}
	return entry.mediation
}

// Capabilities returns every known effect capability, sorted. It is what the
// registry and the docs enumerate, so the list lives in one place.
func Capabilities() []Capability {
	out := make([]Capability, 0, len(catalog))
	for capability := range catalog {
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ParseCapabilities decodes a declared or persisted effect-capability set,
// dropping anything this build does not know.
//
// It mirrors [authz.ParseCapabilities] on purpose: the two namespaces stay
// separate, but "an unknown capability is never a grant" is one rule and it
// should read the same on both axes.
func ParseCapabilities(values []string) []Capability {
	seen := map[Capability]bool{}
	out := make([]Capability, 0, len(values))
	for _, value := range values {
		capability := Capability(strings.TrimSpace(value))
		if !Known(capability) || seen[capability] {
			continue
		}
		seen[capability] = true
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FormatCapabilities encodes an effect-capability set for storage.
func FormatCapabilities(capabilities []Capability) []string {
	values := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		values = append(values, string(capability))
	}
	sort.Strings(values)
	return values
}

// Holds reports whether a set contains a capability.
func Holds(granted []Capability, capability Capability) bool {
	for _, held := range granted {
		if held == capability {
			return true
		}
	}
	return false
}

// manifestCapability is the shape a persisted `required_capabilities` /
// `granted_capabilities` array carries. Only the id is read: a scope object, a
// rationale, and an optional flag are the publisher's and the registry's
// business, and nothing here re-decides them.
type manifestCapability struct {
	ID string `json:"id"`
}

// CapabilitiesFromManifestJSON decodes a persisted capability array from a
// definition binding into effect capability ids.
//
// It is the seam between ADR 0003's manifest fields and this package, and it
// is deliberately lossy in one direction: an id this build does not know is
// dropped, so a binding pinned by an older or newer Tangent can never widen
// what this one will do. Malformed JSON yields nothing rather than an error —
// a binding that cannot be read grants nothing, which is the fail-closed
// reading and does not need a second error path to get right.
func CapabilitiesFromManifestJSON(raw []byte) []Capability {
	if len(raw) == 0 {
		return nil
	}
	var declared []manifestCapability
	if err := json.Unmarshal(raw, &declared); err != nil {
		return nil
	}
	ids := make([]string, 0, len(declared))
	for _, capability := range declared {
		ids = append(ids, capability.ID)
	}
	return ParseCapabilities(ids)
}
