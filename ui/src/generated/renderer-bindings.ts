// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY
// @definition-source sha256:7ea9b8d113d0314bae1375d65b0b3501c9ab1365fb167419f4a49274542a5dfb
// Run `make generate-envelopes` to regenerate.
//
// The renderer binding declared by each of the 22 manifests under
// internal/envelope/extensions/packages/. ADR 0003 §2.3 makes the manifest the
// single answer to "what draws this kind"; ui/src/main.tsx is checked against
// this table rather than being a second source of truth.

/** How a renderer is isolated. */
export type RendererClass =
  | 'react-component'
  | 'declarative'
  | 'sandboxed-frame'
  | 'external-surface';

/** Where a renderer runs, and what the browser lets it reach from there. */
export type RendererIsolation =
  | 'main-origin'
  | 'host-primitive'
  | 'sandboxed-frame'
  | 'external-surface';

/**
 * What one isolation is.
 *
 * Generated from internal/definition/trust.go: a hand-typed policy table in
 * the SPA would be a second decider, and the two would disagree the first
 * time one of them was edited.
 *
 * ADR 0009 reduced the five-value trust class to these four positions and
 * removed the class-based capability ceiling, so this profile no longer
 * carries one.
 */
export interface RendererTrustProfile {
  isolation: RendererIsolation;
  /** False for the classes where no publisher-authored code executes at all. */
  executesPublisherCode: boolean;
  /** Whether this isolation can reach Tangent's own origin, storage, and session. */
  ambientHostAuthority: boolean;
  rendererClasses: readonly string[];
}

export const RENDERER_TRUST_PROFILES: readonly RendererTrustProfile[] = [
  {
    isolation: "main-origin",
    executesPublisherCode: true,
    ambientHostAuthority: true,
    rendererClasses: ["react-component","declarative"],
  },
  {
    isolation: "host-primitive",
    executesPublisherCode: false,
    ambientHostAuthority: false,
    rendererClasses: ["declarative"],
  },
  {
    isolation: "sandboxed-frame",
    executesPublisherCode: true,
    ambientHostAuthority: false,
    rendererClasses: ["sandboxed-frame"],
  },
  {
    isolation: "external-surface",
    executesPublisherCode: false,
    ambientHostAuthority: false,
    rendererClasses: ["external-surface"],
  },
] as const;

export interface RendererBinding {
  /** Wire name of the kind this renderer serves. */
  kind: string;
  version: string;
  /** Stable renderer identity, distinct from kind so one renderer can serve several. */
  rendererId: string;
  rendererClass: RendererClass;
  /** Module specifier and exported symbol, for react-component renderers. */
  entry: string;
  /** Where this renderer runs. Declared by the manifest and never substituted: a declaration the host cannot honor quarantines the definition (ADR 0009). */
  isolation: RendererIsolation;
  /** Manifest inline payload ceiling, after the host cap. The browser-side bound on untrusted display content. */
  inlinePayloadLimitBytes: number;
  /** Declared safe fallback renderer, present only when preserves_meaning is true (ADR 0003 §8 C5). */
  fallbackRendererId: string;
  fallbackPreservesMeaning: boolean;
  fallbackDegradation: string;
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
    kind: "tangent.agent-turn",
    version: "1.1",
    rendererId: "tangent.renderer.turns-inbox",
    rendererClass: "react-component",
    entry: "routes/TurnsInbox#TurnsInbox",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "",
    packageId: "tangent.turns",
    state: "available",
    contractDigest: "sha256:68b8f6e9050b46cb9df6dfeb1b6b4c360914699411f2c8df204f5cac5b9562d8",
  },
  {
    kind: "tangent.app-board",
    version: "0.3",
    rendererId: "tangent.renderer.app-board",
    rendererClass: "react-component",
    entry: "components/envelopes/AppBoard#AppBoard",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "AppBoardView",
    packageId: "tangent.appboard",
    state: "available",
    contractDigest: "sha256:2d917db24c08e3cebc17ae46c2a47ecdee9f4fec519798ed79dec0f7b8dc0c8e",
  },
  {
    kind: "tangent.approval-queue",
    version: "0.8",
    rendererId: "tangent.renderer.approval-queue",
    rendererClass: "react-component",
    entry: "components/envelopes/ApprovalQueue#ApprovalQueue",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "ApprovalQueueView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:dbc7755ca2c7f84d78932ccd0eb4051508fc4fd9fda98e0b11d3c8e5e17504da",
  },
  {
    kind: "tangent.block-draft",
    version: "0.4",
    rendererId: "tangent.renderer.block-draft",
    rendererClass: "react-component",
    entry: "components/envelopes/BlockDraft#BlockDraft",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "BlockDraftView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:ecdfd3188a09c662143db4f18a4434603fbfc545b030a17630c5f6210501e485",
  },
  {
    kind: "tangent.dashboard",
    version: "0.12",
    rendererId: "tangent.renderer.dashboard",
    rendererClass: "react-component",
    entry: "components/envelopes/Dashboard#Dashboard",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "DashboardView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:d98476dab3c21ac23b19c83852be3a3311f6a5130c39a379ddeca00cd058734c",
  },
  {
    kind: "tangent.design-iteration",
    version: "0.3",
    rendererId: "tangent.renderer.design-iteration",
    rendererClass: "sandboxed-frame",
    entry: "components/envelopes/DesignIteration#DesignIteration",
    isolation: "sandboxed-frame",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "DesignIterationView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:bcd36530be7442b0eedb46c297d87e37f0dbb52b28390d25336f32536b98a6b4",
  },
  {
    kind: "tangent.diff-review",
    version: "0.9",
    rendererId: "tangent.renderer.diff-review",
    rendererClass: "react-component",
    entry: "components/envelopes/DiffReview#DiffReview",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "DiffReviewView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:6eee25c706f4eb1c5db3877b10765a55667e38a61986fa93666731b08f97f4d2",
  },
  {
    kind: "tangent.doc-item",
    version: "1.0",
    rendererId: "tangent.renderer.docs-inbox",
    rendererClass: "react-component",
    entry: "routes/DocsInbox#DocsInbox",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "",
    packageId: "tangent.docs",
    state: "available",
    contractDigest: "sha256:d5f723e61d24c275a5296cf6d4dd6fa56c372ded33822b73143d893945d9728f",
  },
  {
    kind: "tangent.external-review",
    version: "0.1",
    rendererId: "tangent.renderer.external-review",
    rendererClass: "react-component",
    entry: "components/envelopes/ExternalReview#ExternalReview",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "ExternalReviewView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:3ff91000a3126d71e4a9b559faf0f5bf850f6ad6443d54c0f78f65f260b06722",
  },
  {
    kind: "tangent.feedback",
    version: "0.3",
    rendererId: "tangent.renderer.feedback",
    rendererClass: "react-component",
    entry: "components/envelopes/Feedback#Feedback",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "FeedbackView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:1baace03c195ee236a0b9299d739794f70eaddcff5828bd0632ff7537facdb9f",
  },
  {
    kind: "tangent.file-picker",
    version: "0.10",
    rendererId: "tangent.renderer.file-picker",
    rendererClass: "react-component",
    entry: "components/envelopes/FilePicker#FilePicker",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "FilePickerView",
    packageId: "tangent.workspace",
    state: "available",
    contractDigest: "sha256:8498aef3e7f1c2c4c24f2174f5be1f16a5a1403865f76effb330f35b9f4d416a",
  },
  {
    kind: "tangent.form-collect",
    version: "0.7",
    rendererId: "tangent.renderer.form-collect",
    rendererClass: "react-component",
    entry: "components/envelopes/FormCollect#FormCollect",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "FormCollectView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:9d100eaf194cc9de56d3b8fbb1af3c16fb273760c60b81d8a023ca46645f6ebe",
  },
  {
    kind: "tangent.hitl-item",
    version: "1.2",
    rendererId: "tangent.renderer.hitl-inbox",
    rendererClass: "react-component",
    entry: "routes/HITLInbox#HITLInbox",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "",
    packageId: "tangent.hitl",
    state: "available",
    contractDigest: "sha256:c2066ad73c1cfdd32e3bc2eaae2eea8cc5988105bee8e91aacecc11af489c3fd",
  },
  {
    kind: "tangent.interview-question",
    version: "0.4",
    rendererId: "tangent.renderer.interview-question",
    rendererClass: "react-component",
    entry: "components/envelopes/InterviewQuestion#InterviewQuestion",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "InterviewQuestionView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:1cf9d36fb09b5f7c0be436c62bfb39a2405c09491bbdb5fe4726f14d116622bb",
  },
  {
    kind: "tangent.output-render",
    version: "0.4",
    rendererId: "tangent.renderer.output-render",
    rendererClass: "react-component",
    entry: "components/envelopes/OutputRender#OutputRender",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "OutputRenderView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:b074ad702ae0de8a970a690d71f5b65cd9ae56944315fb70e2c318392e553840",
  },
  {
    kind: "tangent.progress-panel",
    version: "0.11",
    rendererId: "tangent.renderer.progress-panel",
    rendererClass: "react-component",
    entry: "components/envelopes/ProgressPanel#ProgressPanel",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "ProgressPanelView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:6ac28915e72d3eaf60d849e50888c2d3b3f74f3a78f27698d874e2d766296b59",
  },
  {
    kind: "tangent.prose-revision",
    version: "0.4",
    rendererId: "tangent.renderer.prose-revision",
    rendererClass: "react-component",
    entry: "components/envelopes/ProseRevision#ProseRevision",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "ProseRevisionView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:0a1d37050af23dc3e6e61a6e6aea04dac6720b2644ba4d5f815f015347602f11",
  },
  {
    kind: "tangent.spreadsheet-review",
    version: "0.6",
    rendererId: "tangent.renderer.spreadsheet-review",
    rendererClass: "react-component",
    entry: "components/envelopes/SpreadsheetReview#SpreadsheetReview",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "SpreadsheetReviewView",
    packageId: "tangent.review",
    state: "available",
    contractDigest: "sha256:16282b257eff5d6c7e9ad27ca178980aa5ee7ddb67f1a2edba22af66b3c8c1d9",
  },
  {
    kind: "tangent.synthesis-notes",
    version: "0.4",
    rendererId: "tangent.renderer.synthesis-notes",
    rendererClass: "react-component",
    entry: "components/envelopes/SynthesisNotes#SynthesisNotes",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "SynthesisNotesView",
    packageId: "tangent.writing",
    state: "available",
    contractDigest: "sha256:9884a5ead5775b887689cf468454f8dca1eccc2c2eb0f9633640af85d0381342",
  },
  {
    kind: "tangent.triage",
    version: "0.2",
    rendererId: "tangent.renderer.triage",
    rendererClass: "react-component",
    entry: "components/envelopes/Triage#Triage",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "TriageView",
    packageId: "tangent.generic-candidate",
    state: "available",
    contractDigest: "sha256:8edeb85a3ff1b279b1f7dafa39b49c4a4f9465ff75400e105eaf31c1c8936368",
  },
  {
    kind: "tangent.whiteboard",
    version: "0.5",
    rendererId: "tangent.renderer.whiteboard",
    rendererClass: "react-component",
    entry: "components/envelopes/Whiteboard#Whiteboard",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "WhiteboardView",
    packageId: "tangent.canvas",
    state: "available",
    contractDigest: "sha256:69ffc26511c4df989fb8018feff2b7f488d72a35e1357178290e49ce0e5f7d9c",
  },
  {
    kind: "tangent.wizard",
    version: "0.13",
    rendererId: "tangent.renderer.wizard",
    rendererClass: "react-component",
    entry: "components/envelopes/Wizard#Wizard",
    isolation: "main-origin",
    inlinePayloadLimitBytes: 262144,
    fallbackRendererId: "",
    fallbackPreservesMeaning: false,
    fallbackDegradation: "none",
    component: "WizardView",
    packageId: "tangent.compound",
    state: "available",
    contractDigest: "sha256:8b21894e32f6f8a70afff3c12be89dd8ad7219d7ee6d575f46adabd0c71477d8",
  },
] as const;

/**
 * Look one binding up by wire kind.
 *
 * A kind with no binding has no manifest-declared renderer, which is not a
 * lookup miss to paper over: it means nothing classified the renderer, so
 * nothing may assume it is trusted.
 */
export function rendererBindingFor(kind: string): RendererBinding | null {
  return RENDERER_BINDINGS.find((binding) => binding.kind === kind) ?? null;
}

/** The profile for one isolation, or null when this build does not implement it. */
export function trustProfileFor(isolation: string): RendererTrustProfile | null {
  return RENDERER_TRUST_PROFILES.find((profile) => profile.isolation === isolation) ?? null;
}

/** Kinds whose manifest says a React component in Tangent's own tree draws them. */
export const REACT_COMPONENT_KINDS: readonly string[] = RENDERER_BINDINGS
  .filter((binding) => binding.rendererClass === 'react-component')
  .map((binding) => binding.kind);
