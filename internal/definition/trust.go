package definition

// Renderer isolation, made real.
//
// ADR 0003 §2.3 declared `renderer.trust_class`: a five-value vocabulary whose
// granted class produced two facts, an [Isolation] and a capability ceiling.
// ADR 0009 reduced it. The ceiling is gone and the class is gone with it; what
// a manifest declares now is the isolation itself, which was always the half a
// browser enforced.
//
// # What went, and why the reduction is not a relaxation
//
// The five classes sorted renderers by PROVENANCE — which publisher, verified
// how, shipping its bundle where. Read against the distribution that exists,
// that machinery sorted seventeen first-party React components from one
// first-party React component that imports tldraw, and the two buckets differed
// by `process.exec`: a capability with no executor, gated on an authority
// nothing in the shipped binary holds. The class that blocked an out-of-tree
// publisher blocked it on a signature verifier that was never built. ADR 0009
// has the evidence and the ruling.
//
// So this file no longer decides WHO may supply a renderer. It decides WHERE
// one runs, which is the question a sandbox attribute and a CSP answer.
//
// # What stayed, and is now load-bearing
//
//   - **Isolation** is where the renderer's code executes. It is what the
//     browser enforces, and what makes an effect capability enforceable or
//     merely declared (see effect.MediationFor).
//   - **The shape/isolation coherence check** ([IsolationProfile.AdmitsRendererClass]).
//     A `react-component` calling itself `sandboxed-frame` is not sandboxed, it
//     is mislabeled. This used to be one refusal among several; it is now the
//     only thing standing between a declared isolation and the one in force,
//     which is what lets `renderer.isolation` be read as a fact rather than as
//     a request. Relaxing it would quietly turn the field back into a wish.
//
// Nothing here relaxes the sandbox. `sandboxed-frame` means what it meant:
// `allow-scripts` and nothing else, an opaque origin, and a frame CSP whose
// script-src is a hash of Tangent's own shim. ADR 0009 §2 is explicit that the
// argument retiring the provenance apparatus does not reach it, because the
// agent whose markup lands there is a conduit for content from somewhere else
// rather than the adversary.

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

// IsolationProfile is one isolation's whole answer: whether the code running
// there is the publisher's, and which renderer shapes coherently describe it.
//
// It carries no capability ceiling. ADR 0009 removed the class-based ceiling
// rather than demoting it to documentation: a table that reads like a gate and
// is not one is the defect that ADR rebuilt the model to remove, and keeping it
// one layer down would have reproduced it. What still refuses a capability is
// host policy's own grant and the mediation table, both of which are real.
type IsolationProfile struct {
	// Isolation is the position this profile describes.
	Isolation Isolation
	// ExecutesPublisherCode is false only for the two isolations where nothing
	// the publisher wrote runs as code: `host-primitive` (Tangent's primitives
	// draw a schema) and `external-surface` (Tangent draws nothing).
	ExecutesPublisherCode bool
	// RendererClasses are the renderer shapes coherent with this isolation. A
	// manifest whose shape and isolation disagree is refused at parse time
	// rather than reinterpreted.
	RendererClasses []RendererClass
}

// AdmitsRendererClass reports whether a renderer shape is coherent with this
// isolation.
func (p IsolationProfile) AdmitsRendererClass(class RendererClass) bool {
	for _, admitted := range p.RendererClasses {
		if admitted == class {
			return true
		}
	}
	return false
}

// isolationProfiles is the whole model. Four positions, ordered from the most
// host authority to the least.
var isolationProfiles = map[Isolation]IsolationProfile{
	// Tangent's own document origin. Publisher code that runs here runs with
	// the host's ambient authority — its DOM, storage, cookies and same-origin
	// APIs — because it shipped in `ui_dist` and was reviewed with the release.
	//
	// This is the position `core-trusted` and `portfolio-trusted` both produced
	// before ADR 0009 collapsed them. They ran in the same place, under the same
	// review, differing by a capability nothing performs; the distinction
	// described a supply chain Tangent does not have.
	IsolationMainOrigin: {
		Isolation:             IsolationMainOrigin,
		ExecutesPublisherCode: true,
		RendererClasses:       []RendererClass{RendererReactComponent, RendererDeclarative},
	},

	// No publisher code at all. A declarative renderer supplies data that
	// Tangent's own primitives draw. The drawing code is Tangent's, which makes
	// this the strongest position in the model rather than a middling one:
	// there is nothing untrusted to contain because nothing untrusted executes.
	IsolationHostPrimitive: {
		Isolation:             IsolationHostPrimitive,
		ExecutesPublisherCode: false,
		RendererClasses:       []RendererClass{RendererDeclarative},
	},

	// An opaque-origin frame with no `allow-same-origin`: no access to
	// Tangent's DOM, storage, cookies or same-origin APIs, and no way to reach
	// the effect transport directly.
	//
	// THIS IS THE POSITION THE MODEL EXISTS FOR, and ADR 0009 reduced
	// everything around it without touching it. `tangent.design-iteration`
	// renders markup an agent produced, in the participant's browser, at
	// Tangent's origin, in a session holding their cookie. The agent is not the
	// adversary — it is the conduit, for a web page it summarized or a file it
	// read or a model's output it piped through. "The agent could already run
	// code on the box" is true and does not reach this path.
	IsolationSandboxedFrame: {
		Isolation:             IsolationSandboxedFrame,
		ExecutesPublisherCode: true,
		RendererClasses:       []RendererClass{RendererSandboxedFrame},
	},

	// Not rendered by Tangent at all: the participant is handed off to another
	// application, which returns a result. There is no renderer to contain, and
	// correspondingly no renderer effect to grant.
	IsolationExternalSurface: {
		Isolation:             IsolationExternalSurface,
		ExecutesPublisherCode: false,
		RendererClasses:       []RendererClass{RendererExternalSurface},
	},
}

// IsolationProfileFor returns the profile for an isolation. An unknown value
// has no profile, and every caller treats that as a refusal rather than as a
// default: an isolation this build does not implement can never be declared,
// for the same reason [Assurance.Grantable] refuses an assurance with no
// verifier.
func IsolationProfileFor(isolation Isolation) (IsolationProfile, bool) {
	profile, ok := isolationProfiles[isolation]
	return profile, ok
}

// Isolations returns every implemented isolation, ordered from the most host
// authority to the least. The order is the model, so it is written once here
// rather than re-derived by each caller that wants to print it.
func Isolations() []Isolation {
	return []Isolation{
		IsolationMainOrigin, IsolationHostPrimitive,
		IsolationSandboxedFrame, IsolationExternalSurface,
	}
}
