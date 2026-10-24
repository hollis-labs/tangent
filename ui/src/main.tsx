import { lazy, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import type { AppBoardEnvelope, AppBoardResponse } from "./components/envelopes/AppBoard";
import type {
  ApprovalQueueEnvelope,
  ApprovalQueueResponse,
} from "./components/envelopes/ApprovalQueue";
import type { BlockDraftEnvelope, BlockDraftResponse } from "./components/envelopes/BlockDraft";
import type { DashboardEnvelope, DashboardResponse } from "./components/envelopes/Dashboard";
import type {
  DesignIterationEnvelope,
  DesignIterationResponse,
} from "./components/envelopes/DesignIteration";
import type { DiffReviewEnvelope, DiffReviewResponse } from "./components/envelopes/DiffReview";
import type { FeedbackEnvelope, FeedbackResponse } from "./components/envelopes/Feedback";
import type { FilePickerEnvelope, FilePickerResponse } from "./components/envelopes/FilePicker";
import type { FormCollectEnvelope, FormCollectResponse } from "./components/envelopes/FormCollect";
import type {
  InterviewQuestionEnvelope,
  InterviewQuestionResponse,
} from "./components/envelopes/InterviewQuestion";
import type {
  OutputRenderEnvelope,
  OutputRenderResponse,
} from "./components/envelopes/OutputRender";
import type {
  ProgressPanelEnvelope,
  ProgressPanelResponse,
} from "./components/envelopes/ProgressPanel";
import type {
  ProseRevisionEnvelope,
  ProseRevisionResponse,
} from "./components/envelopes/ProseRevision";
import type {
  SpreadsheetReviewEnvelope,
  SpreadsheetReviewResponse,
} from "./components/envelopes/SpreadsheetReview";
import type {
  SynthesisNotesEnvelope,
  SynthesisNotesResponse,
} from "./components/envelopes/SynthesisNotes";
import type { TriageEnvelope, TriageResponse } from "./components/envelopes/Triage";
import type { WizardEnvelope, WizardResponse } from "./components/envelopes/Wizard";
import { type EnvelopeComponentProps, register } from "./lib/envelope-registry";
import { syncPlugins } from "./lib/plugin-loader";
import { seedShellClientIdentity } from "./lib/ws-client";
import "./index.css";

// Each envelope renderer is its own chunk, fetched when a room first presents
// that kind (EnvelopeRouter supplies the Suspense boundary). Types stay static
// imports and cost nothing at runtime.
const AppBoard = lazy(() =>
  import("./components/envelopes/AppBoard").then((m) => ({ default: m.AppBoard })),
);
const ApprovalQueue = lazy(() =>
  import("./components/envelopes/ApprovalQueue").then((m) => ({ default: m.ApprovalQueue })),
);
const BlockDraft = lazy(() =>
  import("./components/envelopes/BlockDraft").then((m) => ({ default: m.BlockDraft })),
);
const Dashboard = lazy(() =>
  import("./components/envelopes/Dashboard").then((m) => ({ default: m.Dashboard })),
);
const DesignIteration = lazy(() =>
  import("./components/envelopes/DesignIteration").then((m) => ({ default: m.DesignIteration })),
);
const DiffReview = lazy(() =>
  import("./components/envelopes/DiffReview").then((m) => ({ default: m.DiffReview })),
);
const ExternalReview = lazy(() =>
  import("./components/envelopes/ExternalReview").then((m) => ({ default: m.ExternalReview })),
);
const Feedback = lazy(() =>
  import("./components/envelopes/Feedback").then((m) => ({ default: m.Feedback })),
);
const FilePicker = lazy(() =>
  import("./components/envelopes/FilePicker").then((m) => ({ default: m.FilePicker })),
);
const FormCollect = lazy(() =>
  import("./components/envelopes/FormCollect").then((m) => ({ default: m.FormCollect })),
);
const InterviewQuestion = lazy(() =>
  import("./components/envelopes/InterviewQuestion").then((m) => ({
    default: m.InterviewQuestion,
  })),
);
const OutputRender = lazy(() =>
  import("./components/envelopes/OutputRender").then((m) => ({ default: m.OutputRender })),
);
const ProgressPanel = lazy(() =>
  import("./components/envelopes/ProgressPanel").then((m) => ({ default: m.ProgressPanel })),
);
const ProseRevision = lazy(() =>
  import("./components/envelopes/ProseRevision").then((m) => ({ default: m.ProseRevision })),
);
const SpreadsheetReview = lazy(() =>
  import("./components/envelopes/SpreadsheetReview").then((m) => ({
    default: m.SpreadsheetReview,
  })),
);
const SynthesisNotes = lazy(() =>
  import("./components/envelopes/SynthesisNotes").then((m) => ({ default: m.SynthesisNotes })),
);
const Triage = lazy(() =>
  import("./components/envelopes/Triage").then((m) => ({ default: m.Triage })),
);
const Wizard = lazy(() =>
  import("./components/envelopes/Wizard").then((m) => ({ default: m.Wizard })),
);

// v0.1 envelope component registrations. Each component registers
// against the canonical wire `type` string. tangent.triage carries the
// "tangent." plugin prefix because go-envelopes reserves bare names
// for core types (see internal/envelope/extensions/triage.go for the
// matching server-side registration).
//
// The registry is intentionally module-scoped and registered at app
// boot — auto-registration from codegen is a v0.3 concern (one
// component is not enough scaffolding to justify the abstraction).

function TriageAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  // The registry erases the per-component envelope shape on the way
  // through the type → component map. This adapter narrows back to
  // TriageEnvelope at the boundary so the component itself stays
  // strictly typed.
  return (
    <Triage
      envelope={envelope as TriageEnvelope}
      onSubmit={onSubmit as (response: TriageResponse) => void}
      onCancel={onCancel}
    />
  );
}

function ApprovalQueueAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <ApprovalQueue
      envelope={envelope as ApprovalQueueEnvelope}
      onSubmit={onSubmit as (response: ApprovalQueueResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function FeedbackAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <Feedback
      envelope={envelope as FeedbackEnvelope}
      onSubmit={onSubmit as (response: FeedbackResponse) => void}
      onCancel={onCancel}
    />
  );
}

function FormCollectAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <FormCollect
      envelope={envelope as FormCollectEnvelope}
      onSubmit={onSubmit as (response: FormCollectResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function DesignIterationAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <DesignIteration
      envelope={envelope as DesignIterationEnvelope}
      onSubmit={onSubmit as (response: DesignIterationResponse) => void}
      onCancel={onCancel}
    />
  );
}

function InterviewQuestionAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <InterviewQuestion
      envelope={envelope as InterviewQuestionEnvelope}
      onSubmit={onSubmit as (response: InterviewQuestionResponse) => void}
      onCancel={onCancel}
    />
  );
}

function BlockDraftAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <BlockDraft
      envelope={envelope as BlockDraftEnvelope}
      onSubmit={onSubmit as (response: BlockDraftResponse) => void}
      onCancel={onCancel}
    />
  );
}

function ProseRevisionAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <ProseRevision
      envelope={envelope as ProseRevisionEnvelope}
      onSubmit={onSubmit as (response: ProseRevisionResponse) => void}
      onCancel={onCancel}
    />
  );
}

function OutputRenderAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <OutputRender
      envelope={envelope as OutputRenderEnvelope}
      onSubmit={onSubmit as (response: OutputRenderResponse) => void}
      onCancel={onCancel}
    />
  );
}

// The one adapter that threads onDraft. The board is a long-lived surface, so
// what the participant is looking at is recorded as it happens rather than only
// at the moment they decide (ADR 0007 §5).
//
// roomID rides along too: a board whose caller offers a sync route names the
// room in the request, because the route's whole job is to find that room's
// open board and replace it (CW-20260910-0031).
function AppBoardAdapter({
  envelope,
  onSubmit,
  onCancel,
  onDraft,
  roomID,
}: EnvelopeComponentProps) {
  return (
    <AppBoard
      envelope={envelope as AppBoardEnvelope}
      onSubmit={onSubmit as (response: AppBoardResponse) => void}
      onCancel={onCancel}
      onDraft={onDraft}
      roomID={roomID}
    />
  );
}

function DashboardAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <Dashboard
      envelope={envelope as DashboardEnvelope}
      onSubmit={onSubmit as (response: DashboardResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function DiffReviewAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <DiffReview
      envelope={envelope as DiffReviewEnvelope}
      onSubmit={onSubmit as (response: DiffReviewResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function FilePickerAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <FilePicker
      envelope={envelope as FilePickerEnvelope}
      onSubmit={onSubmit as (response: FilePickerResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function ProgressPanelAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <ProgressPanel
      envelope={envelope as ProgressPanelEnvelope}
      onSubmit={onSubmit as (response: ProgressPanelResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function WizardAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <Wizard
      envelope={envelope as WizardEnvelope}
      onSubmit={onSubmit as (response: WizardResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function SpreadsheetReviewAdapter({
  envelope,
  onSubmit,
  onCancel,
  roomID,
}: EnvelopeComponentProps) {
  return (
    <SpreadsheetReview
      envelope={envelope as SpreadsheetReviewEnvelope}
      onSubmit={onSubmit as (response: SpreadsheetReviewResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function SynthesisNotesAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <SynthesisNotes
      envelope={envelope as SynthesisNotesEnvelope}
      onSubmit={onSubmit as (response: SynthesisNotesResponse) => void}
      onCancel={onCancel}
    />
  );
}

register("tangent.external-review", ExternalReview);
register("tangent.triage", TriageAdapter);
register("tangent.approval-queue", ApprovalQueueAdapter);
register("tangent.feedback", FeedbackAdapter);
register("tangent.form-collect", FormCollectAdapter);
register("tangent.design-iteration", DesignIterationAdapter);
register("tangent.interview-question", InterviewQuestionAdapter);
register("tangent.block-draft", BlockDraftAdapter);
register("tangent.prose-revision", ProseRevisionAdapter);
register("tangent.output-render", OutputRenderAdapter);
register("tangent.dashboard", DashboardAdapter);
register("tangent.app-board", AppBoardAdapter);
register("tangent.file-picker", FilePickerAdapter);
register("tangent.progress-panel", ProgressPanelAdapter);
register("tangent.wizard", WizardAdapter);
register("tangent.diff-review", DiffReviewAdapter);
register("tangent.spreadsheet-review", SpreadsheetReviewAdapter);
register("tangent.synthesis-notes", SynthesisNotesAdapter);

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("Tangent: #root not found in index.html");
}

// Before the router's first navigation drops the query string: the desktop
// shell's stable client id and kind ride in on the window URL.
seedShellClientIdentity();

// Sync plugins from /api/plugins/registry (CW-20260911-0035 minimal proof).
// Fire-and-forget: plugin load failures should not block app boot. It runs after the
// first render so it never queues ahead of the first route's own data requests, and
// still within a second so plugin contributions are not noticeably late.
createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
if (typeof window.requestIdleCallback === "function") {
  window.requestIdleCallback(() => void syncPlugins(), { timeout: 1000 });
} else {
  window.setTimeout(() => void syncPlugins(), 0);
}
