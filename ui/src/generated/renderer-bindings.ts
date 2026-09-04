// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY
// @definition-source sha256:641c3e43c015b73bc85c08ca74306ae41a8e4fefcc66c550d05dbcd27f93b98c
// Run `make generate-envelopes` to regenerate.
//
// The renderer binding declared by each of the 18 manifests under
// internal/envelope/extensions/packages/. ADR 0003 §2.3 makes the manifest the
// single answer to "what draws this kind"; ui/src/main.tsx is checked against
// this table rather than being a second source of truth.

/** How a renderer is isolated. */
export type RendererClass =
  | 'react-component'
  | 'declarative'
  | 'sandboxed-frame'
  | 'external-surface';

/** The trust level a manifest requests; the host decides what it grants. */
export type RendererTrustClass =
  | 'core-trusted'
  | 'portfolio-trusted'
  | 'declarative'
  | 'sandboxed-code'
  | 'external-surface';

export interface RendererBinding {
  /** Wire name of the kind this renderer serves. */
  kind: string;
  version: string;
  /** Stable renderer identity, distinct from kind so one renderer can serve several. */
  rendererId: string;
  rendererClass: RendererClass;
  /** Module specifier and exported symbol, for react-component renderers. */
  entry: string;
  trustClass: RendererTrustClass;
  /** Legacy ui.component slug, empty for kinds that render through their own route. */
  component: string;
  packageId: string;
  /** Materialization state at generation time. */
  state: string;
  /** The contract this renderer was generated against. */
  contractDigest: string;
}

export const RENDERER_BINDINGS: readonly RendererBinding[] = [
  {
    kind: "tangent.approval-queue",
    version: "0.7",
    rendererId: "tangent.renderer.approval-queue",
    rendererClass: "react-component",
    entry: "components/envelopes/ApprovalQueue#ApprovalQueue",
    trustClass: "core-trusted",
    component: "ApprovalQueueView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:dbc7755ca2c7f84d78932ccd0eb4051508fc4fd9fda98e0b11d3c8e5e17504da",
  },
  {
    kind: "tangent.block-draft",
    version: "0.3",
    rendererId: "tangent.renderer.block-draft",
    rendererClass: "react-component",
    entry: "components/envelopes/BlockDraft#BlockDraft",
    trustClass: "core-trusted",
    component: "BlockDraftView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:ecdfd3188a09c662143db4f18a4434603fbfc545b030a17630c5f6210501e485",
  },
  {
    kind: "tangent.dashboard",
    version: "0.11",
    rendererId: "tangent.renderer.dashboard",
    rendererClass: "react-component",
    entry: "components/envelopes/Dashboard#Dashboard",
    trustClass: "core-trusted",
    component: "DashboardView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:d98476dab3c21ac23b19c83852be3a3311f6a5130c39a379ddeca00cd058734c",
  },
  {
    kind: "tangent.design-iteration",
    version: "0.2",
    rendererId: "tangent.renderer.design-iteration",
    rendererClass: "sandboxed-frame",
    entry: "components/envelopes/DesignIteration#DesignIteration",
    trustClass: "sandboxed-code",
    component: "DesignIterationView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:bcd36530be7442b0eedb46c297d87e37f0dbb52b28390d25336f32536b98a6b4",
  },
  {
    kind: "tangent.diff-review",
    version: "0.8",
    rendererId: "tangent.renderer.diff-review",
    rendererClass: "react-component",
    entry: "components/envelopes/DiffReview#DiffReview",
    trustClass: "core-trusted",
    component: "DiffReviewView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:6eee25c706f4eb1c5db3877b10765a55667e38a61986fa93666731b08f97f4d2",
  },
  {
    kind: "tangent.feedback",
    version: "0.2",
    rendererId: "tangent.renderer.feedback",
    rendererClass: "react-component",
    entry: "components/envelopes/Feedback#Feedback",
    trustClass: "core-trusted",
    component: "FeedbackView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:1baace03c195ee236a0b9299d739794f70eaddcff5828bd0632ff7537facdb9f",
  },
  {
    kind: "tangent.file-picker",
    version: "0.9",
    rendererId: "tangent.renderer.file-picker",
    rendererClass: "react-component",
    entry: "components/envelopes/FilePicker#FilePicker",
    trustClass: "core-trusted",
    component: "FilePickerView",
    packageId: "tangent.workspace",
    state: "available",
    contractDigest: "sha256:8498aef3e7f1c2c4c24f2174f5be1f16a5a1403865f76effb330f35b9f4d416a",
  },
  {
    kind: "tangent.form-collect",
    version: "0.6",
    rendererId: "tangent.renderer.form-collect",
    rendererClass: "react-component",
    entry: "components/envelopes/FormCollect#FormCollect",
    trustClass: "core-trusted",
    component: "FormCollectView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:a175bb4e256d9c5a224776b8d2910727f7ba0fee03d0f1c24249abe56a81b491",
  },
  {
    kind: "tangent.hitl-item",
    version: "1.0",
    rendererId: "tangent.renderer.hitl-inbox",
    rendererClass: "react-component",
    entry: "routes/HITLInbox#HITLInbox",
    trustClass: "core-trusted",
    component: "",
    packageId: "tangent.hitl",
    state: "available",
    contractDigest: "sha256:e00d61fc2429bc66459f4e55e32a2acc03d735b94268b281ae2550340ab717e8",
  },
  {
    kind: "tangent.interview-question",
    version: "0.3",
    rendererId: "tangent.renderer.interview-question",
    rendererClass: "react-component",
    entry: "components/envelopes/InterviewQuestion#InterviewQuestion",
    trustClass: "core-trusted",
    component: "InterviewQuestionView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:1cf9d36fb09b5f7c0be436c62bfb39a2405c09491bbdb5fe4726f14d116622bb",
  },
  {
    kind: "tangent.output-render",
    version: "0.3",
    rendererId: "tangent.renderer.output-render",
    rendererClass: "react-component",
    entry: "components/envelopes/OutputRender#OutputRender",
    trustClass: "core-trusted",
    component: "OutputRenderView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:b074ad702ae0de8a970a690d71f5b65cd9ae56944315fb70e2c318392e553840",
  },
  {
    kind: "tangent.progress-panel",
    version: "0.10",
    rendererId: "tangent.renderer.progress-panel",
    rendererClass: "react-component",
    entry: "components/envelopes/ProgressPanel#ProgressPanel",
    trustClass: "core-trusted",
    component: "ProgressPanelView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:6ac28915e72d3eaf60d849e50888c2d3b3f74f3a78f27698d874e2d766296b59",
  },
  {
    kind: "tangent.prose-revision",
    version: "0.3",
    rendererId: "tangent.renderer.prose-revision",
    rendererClass: "react-component",
    entry: "components/envelopes/ProseRevision#ProseRevision",
    trustClass: "core-trusted",
    component: "ProseRevisionView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:0a1d37050af23dc3e6e61a6e6aea04dac6720b2644ba4d5f815f015347602f11",
  },
  {
    kind: "tangent.spreadsheet-review",
    version: "0.5",
    rendererId: "tangent.renderer.spreadsheet-review",
    rendererClass: "react-component",
    entry: "components/envelopes/SpreadsheetReview#SpreadsheetReview",
    trustClass: "core-trusted",
    component: "SpreadsheetReviewView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:16282b257eff5d6c7e9ad27ca178980aa5ee7ddb67f1a2edba22af66b3c8c1d9",
  },
  {
    kind: "tangent.synthesis-notes",
    version: "0.3",
    rendererId: "tangent.renderer.synthesis-notes",
    rendererClass: "react-component",
    entry: "components/envelopes/SynthesisNotes#SynthesisNotes",
    trustClass: "core-trusted",
    component: "SynthesisNotesView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:9884a5ead5775b887689cf468454f8dca1eccc2c2eb0f9633640af85d0381342",
  },
  {
    kind: "tangent.triage",
    version: "0.1",
    rendererId: "tangent.renderer.triage",
    rendererClass: "react-component",
    entry: "components/envelopes/Triage#Triage",
    trustClass: "core-trusted",
    component: "TriageView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:8edeb85a3ff1b279b1f7dafa39b49c4a4f9465ff75400e105eaf31c1c8936368",
  },
  {
    kind: "tangent.whiteboard",
    version: "0.4",
    rendererId: "tangent.renderer.whiteboard",
    rendererClass: "react-component",
    entry: "components/envelopes/Whiteboard#Whiteboard",
    trustClass: "portfolio-trusted",
    component: "WhiteboardView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:69ffc26511c4df989fb8018feff2b7f488d72a35e1357178290e49ce0e5f7d9c",
  },
  {
    kind: "tangent.wizard",
    version: "0.12",
    rendererId: "tangent.renderer.wizard",
    rendererClass: "react-component",
    entry: "components/envelopes/Wizard#Wizard",
    trustClass: "core-trusted",
    component: "WizardView",
    packageId: "tangent.compound",
    state: "available",
    contractDigest: "sha256:8b21894e32f6f8a70afff3c12be89dd8ad7219d7ee6d575f46adabd0c71477d8",
  },
] as const;

/** Kinds whose manifest says a React component in Tangent's own tree draws them. */
export const REACT_COMPONENT_KINDS: readonly string[] = RENDERER_BINDINGS
  .filter((binding) => binding.rendererClass === 'react-component')
  .map((binding) => binding.kind);
