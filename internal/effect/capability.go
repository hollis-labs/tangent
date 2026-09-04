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

	// MediationDeclared means the browser hands a renderer in this isolation
	// the same power directly — `navigator.clipboard`, an `<a download>` over
	// a Blob. Routing it through the host produces a declaration, an audit
	// trail, and a scoped path for renderers that cooperate; it is not a
	// barrier against one that does not.
	//
	// It is no longer a property of a capability alone. CW-20260825-0073 made
	// mediation a function of the capability *and* the isolation the renderer
	// runs in — see [MediationFor] — because the same `clipboard.write` is a
	// bare DOM call from Tangent's own origin and an impossibility from an
	// opaque-origin frame. A receipt records the answer for the isolation the
	// request actually came from.
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

	// `connect-src 'self'` on the Tangent document means no renderer, in any
	// isolation, can reach an external origin itself. The host is the only
	// possible actor, in every isolation, so this row is unconditionally
	// enforced — and, with no performer registered, unconditionally refused
	// with `effect_unavailable` rather than admitted and left to the renderer.
	NetworkFetch: {authz.View, MediationHost},

	// The browser still gives a *main-origin* renderer these two directly:
	// `navigator.clipboard` is governed by Permissions Policy rather than CSP,
	// and no CSP directive covers a download at all. [MediationFor] is where
	// that stops being true — inside a sandboxed frame both are impossible, so
	// the entry here is the main-origin answer and the weakest of the two.
	ExportDownload: {authz.View, MediationDeclared},
	ClipboardWrite: {authz.View, MediationDeclared},

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

// MediationOf reports how much of a capability this build enforces for a
// renderer running in Tangent's own origin. An unknown capability is
// [MediationUnimplemented].
//
// It is [MediationFor] at [IsolationMainOrigin], kept as its own name because
// that is the weakest answer the model gives and therefore the safe one for a
// caller that does not know the renderer's isolation.
func MediationOf(capability Capability) Mediation {
	return MediationFor(capability, IsolationMainOrigin)
}

// Isolation is where the requesting renderer's code runs.
//
// It mirrors definition.Isolation value for value and is deliberately a
// separate type in a separate package: internal/definition depends on nothing
// but the standard library and a YAML parser, and internal/effect must not be
// the thing that changes that. TestIsolationVocabulariesAgree holds the two
// sets equal by value across the boundary.
type Isolation string

const (
	// IsolationMainOrigin is Tangent's own document origin.
	IsolationMainOrigin Isolation = "main-origin"
	// IsolationHostPrimitive is a declarative renderer: no publisher code.
	IsolationHostPrimitive Isolation = "host-primitive"
	// IsolationSandboxedFrame is an opaque-origin frame with no
	// `allow-same-origin`, no `allow-downloads`, no `allow-forms`, and a
	// `connect-src 'none'` policy.
	IsolationSandboxedFrame Isolation = "sandboxed-frame"
	// IsolationExternalSurface is a handoff to another application.
	IsolationExternalSurface Isolation = "external-surface"
)

// MediationFor reports how much of a capability the host enforces for a
// renderer in one isolation.
//
// This is the function CW-20260825-0077 said it could not write. Its report
// listed `clipboard.write`, `export.download`, and `network.fetch` as
// permanently `declared`, because "the browser hands a same-origin renderer
// those powers directly and there is no document CSP". Both halves of that
// sentence were true and only one of them is still true, so the answer is now
// a table with two axes rather than one:
//
//	                    main-origin   host-primitive  sandboxed-frame  external
//	file.read_scoped    host          host            host             host
//	file.write_scoped   host          host            host             host
//	evidence.preview    host          host            host             host
//	network.fetch       host          host            host             host
//	export.download     declared      host            host             host
//	clipboard.write     declared      host            host             host
//	process.exec        unimplemented (every isolation)
//
// The three interesting cells, and exactly what makes each one true:
//
//   - **`network.fetch` at main-origin is now genuinely enforced.** The
//     document carries `connect-src 'self' ws://<host> wss://<host>`
//     (internal/server/security.go). CSP's `connect-src` is the one directive
//     that governs `fetch`, `XMLHttpRequest`, `WebSocket`, `EventSource`, and
//     `navigator.sendBeacon` alike, so a renderer in Tangent's origin can no
//     longer reach an external origin at all. `network.fetch` means "retrieve
//     an external origin's bytes"; the browser now refuses that, and the host
//     is the only remaining possible actor. There is no performer registered
//     for it, so the honest consequence is that a declared and granted
//     `network.fetch` is refused with `effect_unavailable` instead of being
//     admitted and silently performed by the renderer.
//
//   - **`clipboard.write` at main-origin is still only declared.** CSP has no
//     clipboard directive; the control is Permissions Policy, whose
//     `clipboard-write` allowlist Tangent sets to `(self)`. `self` is exactly
//     the main origin, so the policy denies every frame and permits the host's
//     own tree — which is the enforcement a sandboxed renderer meets and the
//     absence of one a main-origin renderer meets. Setting it to `()` would
//     enforce it, and would break three shipped core-trusted components that
//     copy to the clipboard today; that is a capability backfill, not a trust
//     boundary, and it is named in the limitations rather than done here.
//
//   - **`export.download` at main-origin is still only declared.** A
//     same-origin renderer can build a Blob and click an `<a download>`. No
//     CSP directive covers downloads; the only browser control is the `sandbox`
//     token `allow-downloads`, which is a property of a frame and cannot be
//     applied to the top-level document without sandboxing the whole
//     application.
//
// `host-primitive` returns `host` for every implemented capability for a
// different reason than the frame does: there is no publisher code in that
// isolation at all, so nothing but the host can act.
//
// The zero Isolation is [IsolationMainOrigin]. An unset field must never buy a
// stronger claim than the model has evidence for, and main-origin is the
// weakest cell in every row.
func MediationFor(capability Capability, isolation Isolation) Mediation {
	entry, ok := catalog[capability]
	if !ok {
		return MediationUnimplemented
	}
	if entry.mediation != MediationDeclared {
		return entry.mediation
	}
	switch isolation {
	case IsolationMainOrigin, "":
		return MediationDeclared
	default:
		return MediationHost
	}
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
