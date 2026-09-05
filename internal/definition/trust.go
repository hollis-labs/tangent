package definition

import (
	"fmt"
	"sort"
	"strings"
)

// Renderer trust classes, made real.
//
// ADR 0003 §2.3 declares `renderer.trust_class` and says "a manifest requests a
// class; Tangent never grants more than the trust evidence in §2.7 supports".
// Until this file the field was a validated string and nothing more: every
// renderer, whatever it declared, ran in the same React tree with the same
// authority. This file is the other half — what each class *may* do, and the
// two facts an enforcement layer reads off it.
//
// Those two facts are [Isolation] and the capability ceiling, and they are
// deliberately separate:
//
//   - **Isolation** is where the renderer's code executes. It is what the
//     browser enforces, and it is what makes an effect capability enforceable
//     or merely declared (see effect.MediationFor). A class is not "more
//     trusted" because it says so; it is less contained because it runs
//     somewhere with more authority.
//   - **The capability ceiling** is what a class may ever *declare*. It is
//     evaluated at materialization, before host policy's own grant, so a
//     publisher cannot reach a capability by having an operator grant it: the
//     class has to permit it first.
//
// The ordering is strict and total on the ceiling:
//
//	core-trusted ⊃ portfolio-trusted ⊃ sandboxed-code ⊃ declarative = external-surface = ∅
//
// which is what makes "raising renderer.trust_class is a major version bump"
// (§8 C2) a statement about a set rather than about a label.

// Isolation is where a renderer's code actually runs.
//
// It is a separate vocabulary from [RendererClass] on purpose. `RendererClass`
// is the publisher's answer to "what shape is my renderer"; `Isolation` is the
// host's answer to "what can the browser reach from there", and the second is
// derived from the granted trust class rather than from the declared shape. A
// publisher that mislabels its renderer's shape cannot thereby widen its
// isolation.
type Isolation string

const (
	// IsolationMainOrigin is Tangent's own document origin: the React tree,
	// its storage, its cookies, and every same-origin API. Publisher code that
	// runs here is running with the host's ambient authority, so only a class
	// whose material ships and is reviewed with the release may reach it.
	IsolationMainOrigin Isolation = "main-origin"

	// IsolationHostPrimitive is "no publisher code at all". A declarative
	// renderer supplies data that Tangent's own trusted primitives draw. It is
	// main-origin in the sense that the *drawing* code is Tangent's, and it is
	// the strongest position in the model: there is nothing untrusted to
	// contain because nothing untrusted executes.
	IsolationHostPrimitive Isolation = "host-primitive"

	// IsolationSandboxedFrame is an opaque-origin frame with no
	// `allow-same-origin`: no access to Tangent's DOM, storage, cookies, or
	// same-origin APIs, and no way to reach the effect transport directly.
	// Every effect a sandboxed renderer causes is performed by the host shell
	// on its behalf, which is what makes the declared-vs-enforced distinction
	// flip for this class.
	IsolationSandboxedFrame Isolation = "sandboxed-frame"

	// IsolationExternalSurface is not rendered by Tangent at all: the
	// participant is handed off to another application, which returns a
	// result. There is no renderer to contain, and correspondingly no renderer
	// effect to grant.
	IsolationExternalSurface Isolation = "external-surface"
)

func (i Isolation) valid() bool {
	switch i {
	case IsolationMainOrigin, IsolationHostPrimitive, IsolationSandboxedFrame, IsolationExternalSurface:
		return true
	}
	return false
}

// AmbientHostAuthority reports whether code in this isolation can reach
// Tangent's own origin — its DOM, its storage, its cookies, and the
// same-origin APIs that carry the participant session.
//
// This is the predicate acceptance criterion 1 is written against: "untrusted
// code never runs with Tangent main-origin authority or ambient MCP, storage,
// filesystem, or network access". It is a property of the isolation, never of
// the label, so it cannot be widened by a manifest.
func (i Isolation) AmbientHostAuthority() bool {
	return i == IsolationMainOrigin
}

// TrustProfile is one trust class's whole answer: where it runs, whether its
// code is the publisher's, and what it may declare.
type TrustProfile struct {
	// Class is the trust class this profile describes.
	Class TrustClass
	// Isolation is where a renderer granted this class executes.
	Isolation Isolation
	// ExecutesPublisherCode is false only for the two classes where nothing
	// the publisher wrote runs as code: `declarative` (Tangent's primitives
	// draw a schema) and `external-surface` (Tangent draws nothing).
	ExecutesPublisherCode bool
	// RendererClasses are the renderer shapes this class admits. A manifest
	// whose class and trust class disagree is refused at parse time rather
	// than reinterpreted, because a `react-component` that calls itself
	// `sandboxed-code` is not sandboxed — it is mislabeled.
	RendererClasses []RendererClass
	// Capabilities is the ceiling: the host-mediated effect capability ids a
	// manifest in this class may declare at all. Host policy still has to
	// grant each one; the ceiling is the earlier, publisher-independent gate.
	//
	// The authoritative spelling of these ids lives in internal/effect's
	// catalog. TestTheTrustCeilingNamesOnlyKnownCapabilities holds the two
	// lists together across the package boundary.
	Capabilities []string
	// RequiresReleaseProvenance marks the classes that may only be granted to
	// material that ships and is reviewed with the Tangent release (§7 T6).
	RequiresReleaseProvenance bool
}

// Permits reports whether this class may declare a capability id.
func (p TrustProfile) Permits(capabilityID string) bool {
	id := strings.TrimSpace(capabilityID)
	for _, permitted := range p.Capabilities {
		if permitted == id {
			return true
		}
	}
	return false
}

// AdmitsRendererClass reports whether a renderer shape is coherent with this
// trust class.
func (p TrustProfile) AdmitsRendererClass(class RendererClass) bool {
	for _, admitted := range p.RendererClasses {
		if admitted == class {
			return true
		}
	}
	return false
}

// The capability ids each class may declare. Spelled as literals rather than
// imported from internal/effect so this package keeps the stdlib-only
// dependency its doc comment promises; the cross-package test is what keeps
// them honest.
const (
	capabilityFileRead        = "file.read_scoped"
	capabilityFileWrite       = "file.write_scoped"
	capabilityEvidencePreview = "evidence.preview"
	capabilityExportDownload  = "export.download"
	capabilityClipboardWrite  = "clipboard.write"
	capabilityNetworkFetch    = "network.fetch"
	capabilityProcessExec     = "process.exec"
)

// trustProfiles is the whole model. Reading it top to bottom is reading the
// direction document's five classes in decreasing order of what they may
// reach.
var trustProfiles = map[TrustClass]TrustProfile{
	// Shipped and reviewed with the Tangent release. It is the only class that
	// runs publisher code in Tangent's own origin *and* may name every
	// capability, and both of those follow from the same fact: this material
	// went through Tangent's review, so there is no publisher-vs-host trust
	// boundary left to enforce inside the browser.
	//
	// `process.exec` is in the ceiling and remains refused twice over by the
	// broker (no executor, and an object precondition nothing holds). The
	// ceiling is about what a class may *declare*; keeping the reserved
	// capability nameable by exactly one class is what makes the ordering
	// below strict rather than decorative.
	TrustCoreTrusted: {
		Class:                 TrustCoreTrusted,
		Isolation:             IsolationMainOrigin,
		ExecutesPublisherCode: true,
		RendererClasses:       []RendererClass{RendererReactComponent, RendererDeclarative},
		Capabilities: []string{
			capabilityFileRead, capabilityFileWrite, capabilityEvidencePreview,
			capabilityExportDownload, capabilityClipboardWrite, capabilityNetworkFetch,
			capabilityProcessExec,
		},
		RequiresReleaseProvenance: true,
	},

	// A signed Hollis Labs package. It runs in the main origin like
	// core-trusted, because a portfolio package is reviewed by the same people
	// — but it may not name the reserved process capability, and it is the
	// class an out-of-tree publisher can actually reach, so its evidence
	// requirement is where a signature verifier will attach.
	//
	// **There is no signature verifier in v0.x.** `signed-package` is not
	// [Assurance.Grantable], so an out-of-tree portfolio package is refused at
	// the assurance gate rather than admitted unverified. The consequence is
	// honest and worth stating: for the material that ships in this tree,
	// portfolio-trusted is today enforced exactly as strictly as core-trusted
	// and no more. What separates them is the ceiling and the provenance rule,
	// not a cryptographic check.
	TrustPortfolioTrusted: {
		Class:                 TrustPortfolioTrusted,
		Isolation:             IsolationMainOrigin,
		ExecutesPublisherCode: true,
		RendererClasses:       []RendererClass{RendererReactComponent, RendererDeclarative},
		Capabilities: []string{
			capabilityFileRead, capabilityFileWrite, capabilityEvidencePreview,
			capabilityExportDownload, capabilityClipboardWrite, capabilityNetworkFetch,
		},
		RequiresReleaseProvenance: false,
	},

	// Schema-driven UI drawn by Tangent's own primitives. No publisher code
	// executes anywhere, which is why the ceiling is empty rather than small:
	// an effect capability is a grant to *a renderer*, and this class has no
	// renderer of its own to grant it to. A declarative definition that needs
	// an effect is asking to be a different class.
	TrustDeclarative: {
		Class:                 TrustDeclarative,
		Isolation:             IsolationHostPrimitive,
		ExecutesPublisherCode: false,
		RendererClasses:       []RendererClass{RendererDeclarative},
		Capabilities:          nil,
	},

	// An isolated extension with no ambient host authority. Publisher code
	// runs, and it is untrusted, so it runs in an opaque-origin frame that
	// cannot reach Tangent's DOM, storage, cookies, or transport.
	//
	// It may name the mediated read and presentation capabilities, because in
	// this isolation they are genuinely host-performed: the frame cannot fetch,
	// download, or write a clipboard itself, so a grant is the *only* path and
	// refusing the grant refuses the effect.
	//
	// `file.write_scoped` is deliberately absent. Reading content a participant
	// is already entitled to see is one thing; letting untrusted code author
	// bytes into the operator's workspace through a host proxy is the effect
	// the sandbox exists to prevent, and no scoping makes it safe enough to
	// hand to unreviewed code.
	TrustSandboxedCode: {
		Class:                 TrustSandboxedCode,
		Isolation:             IsolationSandboxedFrame,
		ExecutesPublisherCode: true,
		RendererClasses:       []RendererClass{RendererSandboxedFrame},
		Capabilities: []string{
			capabilityFileRead, capabilityEvidencePreview,
			capabilityExportDownload, capabilityClipboardWrite, capabilityNetworkFetch,
		},
	},

	// A linked application owns the rendering and returns a result through a
	// narrow handoff. Tangent draws nothing, so there is no renderer to grant
	// anything to. Whatever the external surface may do, it does on its own
	// authority in its own origin, and Tangent's capability model has no
	// opinion about it — which is precisely why nothing here may be described
	// as a Tangent grant.
	TrustExternalSurface: {
		Class:                 TrustExternalSurface,
		Isolation:             IsolationExternalSurface,
		ExecutesPublisherCode: false,
		RendererClasses:       []RendererClass{RendererExternalSurface},
		Capabilities:          nil,
	},
}

// TrustProfileFor returns the profile for a class. An unknown class has no
// profile, and every caller treats that as a refusal rather than as a default:
// a trust class this build does not implement can never be granted, for the
// same reason [Assurance.Grantable] refuses an assurance with no verifier.
func TrustProfileFor(class TrustClass) (TrustProfile, bool) {
	profile, ok := trustProfiles[class]
	return profile, ok
}

// TrustClasses returns every implemented trust class, ordered from the most
// authority to the least. The order is the model, so it is written once here
// rather than re-derived by each caller that wants to print it.
func TrustClasses() []TrustClass {
	return []TrustClass{
		TrustCoreTrusted, TrustPortfolioTrusted, TrustDeclarative,
		TrustSandboxedCode, TrustExternalSurface,
	}
}

// IsolationFor returns where a renderer granted this class runs.
//
// An unknown class returns [IsolationExternalSurface] — the position with no
// Tangent-granted authority at all — so a class this build cannot reason about
// never resolves to the main origin by omission.
func IsolationFor(class TrustClass) Isolation {
	profile, ok := trustProfiles[class]
	if !ok {
		return IsolationExternalSurface
	}
	return profile.Isolation
}

// trustEvidenceSupports reports whether a manifest's provenance supports the
// trust class it requests, and why not when it does not.
//
// This is §2.7's "Tangent never grants more than the trust evidence supports",
// and it is the check that has teeth for material from outside this tree.
// Three facts are consulted, and each one is a fact about where the material
// came from rather than about what it says of itself:
//
//  1. The assurance must be one this build can actually verify
//     ([Assurance.Grantable]). Materialize has already refused a
//     non-grantable assurance before this runs; the check is repeated as a
//     precondition because a future caller that skips that step must not
//     silently get a grant.
//  2. A class requiring release provenance must be published by Tangent or by
//     the go-envelopes core. An application package from another publisher
//     cannot request core-trusted, however it is reviewed downstream — §7 T6.
//  3. A class requiring release provenance must ship *with* the release: an
//     authored `renderer.asset_digest` means the bundle is distributed
//     separately, and a separately-distributed bundle is by definition not the
//     one that went through the release review.
func trustEvidenceSupports(manifest *Manifest, assurance Assurance, profile TrustProfile) (bool, string) {
	if !assurance.Grantable() {
		return false, fmt.Sprintf(
			"renderer.trust_class %q needs verified trust evidence and assurance %q has no verifier in this build",
			profile.Class, assurance)
	}
	if !profile.RequiresReleaseProvenance {
		return true, ""
	}
	if manifest.Publisher != TangentPublisher && manifest.Publisher != CorePublisher {
		return false, fmt.Sprintf(
			"renderer.trust_class %q is reserved for material reviewed with the Tangent release; "+
				"publisher %q is not %q or %q",
			profile.Class, manifest.Publisher, TangentPublisher, CorePublisher)
	}
	if strings.TrimSpace(manifest.Renderer.AssetDigest) != "" {
		return false, fmt.Sprintf(
			"renderer.trust_class %q is reserved for renderers built with the release, "+
				"and renderer.asset_digest names a separately distributed bundle",
			profile.Class)
	}
	return true, ""
}

// trustCeilingDenials returns the declared capabilities this trust class may
// not hold, in a stable order.
//
// It runs *before* host policy's intersection, and that ordering is the
// decision. A publisher may not reach a capability by persuading an operator
// to grant it: the class has to permit it first, and an operator's grant
// widens nothing the class already refused.
func trustCeilingDenials(required []Capability, profile TrustProfile) []Capability {
	var denied []Capability
	for _, capability := range required {
		if !profile.Permits(capability.ID) {
			denied = append(denied, capability)
		}
	}
	sort.Slice(denied, func(i, j int) bool { return denied[i].ID < denied[j].ID })
	return denied
}
