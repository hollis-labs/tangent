package extensions

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/definition"
)

// This file is the gate CW-20260911-0008 found missing.
//
// ADR 0003 §3 has said since 2026-09-04 that a `revision` may advance within a
// `version` only while `contract_digest`, `renderer.class`,
// `renderer.trust_class` and `required_capabilities` all hold. Nothing compared
// those, so `tangent.app-board` spent three revisions adding request-schema
// fields at `version: "0.1"` — each one a version bump by the rule — and no
// test noticed for a year. The rule was right and the repo was wrong; §3 was
// not amended, and this is the mechanism it lacked.
//
// # Why a committed table and not a derivation
//
// "The contract moved" is a comparison against what was published before, and
// a build has no history. Deriving the previous identity from the manifest
// would be comparing the manifest to itself, which asserts nothing — the same
// reason TestEveryShippedRendererIsExplicitlyClassified writes its trust
// classes out kind by kind rather than reading them back out of the files it
// is checking.
//
// So the previous publication is committed here, and moving an entry is the
// deliberate act. There is deliberately no `make` target that rewrites this
// table: a gate whose baseline can be refreshed by running a command is a gate
// that gets refreshed instead of read. What a failure prints is the exact
// literal to paste, which costs a reviewer one diff hunk and costs an author
// nothing they should not be spending.

// lockedPublication is one kind's last reviewed publication. The four identity
// fields are ADR 0003 §3's, verbatim; `response` is the marker the one granted
// exception turns on, and is not itself frozen.
type lockedPublication struct {
	version   string
	revision  int64
	contract  string
	class     definition.RendererClass
	isolation definition.Isolation
	// capabilities is the digest over required_capabilities, empty for a kind
	// requiring none — which is every kind in this build. A non-empty value
	// here is the first real capability grant in the distribution and should
	// not arrive quietly.
	capabilities string
	response     definition.ResponseSchemaCompatibility
}

// contractLock is the committed record. Every shipped kind has an entry, and a
// kind added or removed without touching this table fails the build.
var contractLock = map[string]lockedPublication{
	AgentTurnEnvelopeType: {
		version: "1.0", revision: 1,
		contract:  "sha256:8b840ca69c1d800d4926f984ceb2f593be62f0595d39db698404534eb5256b76",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaPresent,
	},
	AppBoardEnvelopeType: {
		// 0.2 is CW-20260911-0008's one-minor-bump reconciliation: the `sync`
		// block, `sync.note_label` and `cards[].note` were each added under a
		// revision bump at 0.1, and each moved the contract. One version covers
		// all three because semver versions mark releases and none of them were
		// released separately. The manifest says so at the field.
		version: "0.3", revision: 1,
		contract:  "sha256:2d917db24c08e3cebc17ae46c2a47ecdee9f4fec519798ed79dec0f7b8dc0c8e",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	ApprovalQueueEnvelopeType: {
		version: "0.8", revision: 1,
		contract:  "sha256:dbc7755ca2c7f84d78932ccd0eb4051508fc4fd9fda98e0b11d3c8e5e17504da",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	BlockDraftEnvelopeType: {
		version: "0.4", revision: 1,
		contract:  "sha256:ecdfd3188a09c662143db4f18a4434603fbfc545b030a17630c5f6210501e485",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	DashboardEnvelopeType: {
		version: "0.12", revision: 1,
		contract:  "sha256:d98476dab3c21ac23b19c83852be3a3311f6a5130c39a379ddeca00cd058734c",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	DesignIterationEnvelopeType: {
		version: "0.3", revision: 1,
		contract:  "sha256:bcd36530be7442b0eedb46c297d87e37f0dbb52b28390d25336f32536b98a6b4",
		class:     definition.RendererSandboxedFrame,
		isolation: definition.IsolationSandboxedFrame,
		response:  definition.ResponseSchemaAbsent,
	},
	DiffReviewEnvelopeType: {
		version: "0.9", revision: 1,
		contract:  "sha256:6eee25c706f4eb1c5db3877b10765a55667e38a61986fa93666731b08f97f4d2",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	DocItemEnvelopeType: {
		version: "1.0", revision: 1,
		contract:  "sha256:d5f723e61d24c275a5296cf6d4dd6fa56c372ded33822b73143d893945d9728f",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaPresent,
	},
	FeedbackEnvelopeType: {
		version: "0.3", revision: 1,
		contract:  "sha256:1baace03c195ee236a0b9299d739794f70eaddcff5828bd0632ff7537facdb9f",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	FilePickerEnvelopeType: {
		version: "0.10", revision: 1,
		contract:  "sha256:8498aef3e7f1c2c4c24f2174f5be1f16a5a1403865f76effb330f35b9f4d416a",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	FormCollectEnvelopeType: {
		// revision 2 at version 0.6 is ADR 0003 §3's one granted exception,
		// used: the once-per-kind `compatibility_response_schema: absent` →
		// `present` backfill (A1, CW-20260825-0074). CheckRevisionAdvance
		// honors it from the marker below rather than hard-failing it, which is
		// why this entry is the one place in the table where a moved contract
		// and a held version are not a finding.
		version: "0.7", revision: 1,
		contract:  "sha256:9d100eaf194cc9de56d3b8fbb1af3c16fb273760c60b81d8a023ca46645f6ebe",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaPresent,
	},
	HITLItemEnvelopeType: {
		version: "1.2", revision: 1,
		contract:  "sha256:c2066ad73c1cfdd32e3bc2eaae2eea8cc5988105bee8e91aacecc11af489c3fd",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaPresent,
	},
	InterviewQuestionEnvelopeType: {
		version: "0.4", revision: 1,
		contract:  "sha256:1cf9d36fb09b5f7c0be436c62bfb39a2405c09491bbdb5fe4726f14d116622bb",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	OutputRenderEnvelopeType: {
		version: "0.4", revision: 1,
		contract:  "sha256:b074ad702ae0de8a970a690d71f5b65cd9ae56944315fb70e2c318392e553840",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	ProgressPanelEnvelopeType: {
		version: "0.11", revision: 1,
		contract:  "sha256:6ac28915e72d3eaf60d849e50888c2d3b3f74f3a78f27698d874e2d766296b59",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	ProseRevisionEnvelopeType: {
		version: "0.4", revision: 1,
		contract:  "sha256:0a1d37050af23dc3e6e61a6e6aea04dac6720b2644ba4d5f815f015347602f11",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	SpreadsheetReviewEnvelopeType: {
		version: "0.6", revision: 1,
		contract:  "sha256:16282b257eff5d6c7e9ad27ca178980aa5ee7ddb67f1a2edba22af66b3c8c1d9",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	SynthesisNotesEnvelopeType: {
		version: "0.4", revision: 1,
		contract:  "sha256:9884a5ead5775b887689cf468454f8dca1eccc2c2eb0f9633640af85d0381342",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	TriageEnvelopeType: {
		version: "0.2", revision: 1,
		contract:  "sha256:8edeb85a3ff1b279b1f7dafa39b49c4a4f9465ff75400e105eaf31c1c8936368",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	WhiteboardEnvelopeType: {
		version: "0.5", revision: 1,
		contract:  "sha256:69ffc26511c4df989fb8018feff2b7f488d72a35e1357178290e49ce0e5f7d9c",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
	WizardEnvelopeType: {
		version: "0.13", revision: 1,
		contract:  "sha256:8b21894e32f6f8a70afff3c12be89dd8ad7219d7ee6d575f46adabd0c71477d8",
		class:     definition.RendererReactComponent,
		isolation: definition.IsolationMainOrigin,
		response:  definition.ResponseSchemaAbsent,
	},
}

// TestNoRevisionAdvancedWhileTheContractMoved is the ADR 0003 §3 gate.
//
// It fails in two distinguishable ways, and the message says which:
//
//   - **A §3 violation.** The kind holds its version while one of the four
//     protected fields moved. That is a version bump, and the fix is to bump
//     the version rather than the entry below.
//   - **A stale lock.** The kind legally moved — a version bump, or a revision
//     bump over an unchanged contract — and this table has not caught up. The
//     fix is to paste the printed literal.
//
// Both fail the build. The second is routine and one line; the first is the
// year of silent divergence this gate exists to end.
func TestNoRevisionAdvancedWhileTheContractMoved(t *testing.T) {
	t.Parallel()
	svc := registeredService(t)

	live := map[string]definition.Publication{}
	for _, item := range svc.MaterializedDefinitions() {
		live[item.Manifest.Kind] = definition.PublicationOf(item.Manifest, item.Derived)
	}

	for kind, current := range live {
		entry, locked := contractLock[kind]
		if !locked {
			t.Errorf("%s ships a manifest with no entry in contractLock; add:\n%s",
				kind, lockLiteral(kind, current))
			continue
		}
		previous := entry.publication(kind)

		// §3 first. A stale-lock message on top of a rule violation would bury
		// the finding under a paste-this-literal instruction, which is the
		// specific way a gate teaches people to stop reading it.
		if err := definition.CheckRevisionAdvance(previous, current); err != nil {
			if !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
				t.Errorf("%s: %v", kind, err)
				continue
			}
			t.Errorf("%s violates ADR 0003 §3: %v\n\n"+
				"Bump `version` in the manifest — do NOT update contractLock to match.\n"+
				"`revision` is a non-semantic edit counter within a version; a moved contract is a "+
				"new version, and a new version starts its revision count at 1.", kind, err)
			continue
		}

		if previous != current {
			t.Errorf("%s moved legally and contractLock is stale; replace its entry with:\n%s",
				kind, lockLiteral(kind, current))
		}
	}

	for kind := range contractLock {
		if _, shipped := live[kind]; !shipped {
			t.Errorf("contractLock pins %s, which this build no longer ships; remove the entry", kind)
		}
	}
}

// publication projects a locked entry into the shape the rule compares.
func (l lockedPublication) publication(kind string) definition.Publication {
	return definition.Publication{
		Kind:     kind,
		Version:  l.version,
		Revision: l.revision,
		Identity: definition.Identity{
			ContractDigest:     l.contract,
			RendererClass:      l.class,
			RendererIsolation:  l.isolation,
			CapabilitiesDigest: l.capabilities,
		},
		ResponseSchema: l.response,
	}
}

// lockLiteral renders one live publication as the Go literal to paste into
// contractLock. A gate that reports a digest mismatch without saying what to
// write is a gate whose remedy is a transcription exercise.
func lockLiteral(kind string, current definition.Publication) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\t%s: {\n", constantFor(kind))
	fmt.Fprintf(&out, "\t\tversion: %q, revision: %d,\n", current.Version, current.Revision)
	fmt.Fprintf(&out, "\t\tcontract: %q,\n", current.Identity.ContractDigest)
	fmt.Fprintf(&out, "\t\tclass:     definition.%s,\n", rendererClassConstants[current.Identity.RendererClass])
	fmt.Fprintf(&out, "\t\tisolation: definition.%s,\n", isolationConstants[current.Identity.RendererIsolation])
	if current.Identity.CapabilitiesDigest != "" {
		fmt.Fprintf(&out, "\t\tcapabilities: %q,\n", current.Identity.CapabilitiesDigest)
	}
	fmt.Fprintf(&out, "\t\tresponse: definition.%s,\n", responseSchemaConstants[current.ResponseSchema])
	out.WriteString("\t},")
	return out.String()
}

// The three lookup tables below exist so the printed literal is code a reader
// can paste rather than a quoted string they have to translate back into a
// constant. A value missing from one of them is a new enum member, and the
// fallback names it rather than emitting something that will not compile.
var rendererClassConstants = map[definition.RendererClass]string{
	definition.RendererReactComponent:  "RendererReactComponent",
	definition.RendererDeclarative:     "RendererDeclarative",
	definition.RendererSandboxedFrame:  "RendererSandboxedFrame",
	definition.RendererExternalSurface: "RendererExternalSurface",
}

var isolationConstants = map[definition.Isolation]string{
	definition.IsolationMainOrigin:      "IsolationMainOrigin",
	definition.IsolationHostPrimitive:   "IsolationHostPrimitive",
	definition.IsolationSandboxedFrame:  "IsolationSandboxedFrame",
	definition.IsolationExternalSurface: "IsolationExternalSurface",
}

var responseSchemaConstants = map[definition.ResponseSchemaCompatibility]string{
	definition.ResponseSchemaAbsent:  "ResponseSchemaAbsent",
	definition.ResponseSchemaPresent: "ResponseSchemaPresent",
}

// constantFor maps a wire name back to the exported Go constant this package
// spells it with, so the printed literal keys on the same identifier every
// other table in this package does.
func constantFor(kind string) string {
	for _, candidate := range kindConstants {
		if candidate.kind == kind {
			return candidate.constant
		}
	}
	return fmt.Sprintf("%q", kind)
}

var kindConstants = []struct{ kind, constant string }{
	{AgentTurnEnvelopeType, "AgentTurnEnvelopeType"},
	{AppBoardEnvelopeType, "AppBoardEnvelopeType"},
	{ApprovalQueueEnvelopeType, "ApprovalQueueEnvelopeType"},
	{BlockDraftEnvelopeType, "BlockDraftEnvelopeType"},
	{DashboardEnvelopeType, "DashboardEnvelopeType"},
	{DesignIterationEnvelopeType, "DesignIterationEnvelopeType"},
	{DiffReviewEnvelopeType, "DiffReviewEnvelopeType"},
	{DocItemEnvelopeType, "DocItemEnvelopeType"},
	{FeedbackEnvelopeType, "FeedbackEnvelopeType"},
	{FilePickerEnvelopeType, "FilePickerEnvelopeType"},
	{FormCollectEnvelopeType, "FormCollectEnvelopeType"},
	{HITLItemEnvelopeType, "HITLItemEnvelopeType"},
	{InterviewQuestionEnvelopeType, "InterviewQuestionEnvelopeType"},
	{OutputRenderEnvelopeType, "OutputRenderEnvelopeType"},
	{ProgressPanelEnvelopeType, "ProgressPanelEnvelopeType"},
	{ProseRevisionEnvelopeType, "ProseRevisionEnvelopeType"},
	{SpreadsheetReviewEnvelopeType, "SpreadsheetReviewEnvelopeType"},
	{SynthesisNotesEnvelopeType, "SynthesisNotesEnvelopeType"},
	{TriageEnvelopeType, "TriageEnvelopeType"},
	{WhiteboardEnvelopeType, "WhiteboardEnvelopeType"},
	{WizardEnvelopeType, "WizardEnvelopeType"},
}

// TestEveryShippedKindHasAKindConstant keeps the printer honest. A kind absent
// from kindConstants would be reported as a quoted string, and the literal a
// failing gate printed would not compile — which is how a remedy becomes a
// second bug to debug during the fix.
func TestEveryShippedKindHasAKindConstant(t *testing.T) {
	t.Parallel()
	registered := RegisteredTypes()
	sort.Strings(registered)
	for _, kind := range registered {
		if constantFor(kind) == fmt.Sprintf("%q", kind) {
			t.Errorf("%s has no entry in kindConstants", kind)
		}
	}
	if len(kindConstants) != len(registered) {
		t.Errorf("kindConstants has %d entries, the build ships %d kinds",
			len(kindConstants), len(registered))
	}
}
