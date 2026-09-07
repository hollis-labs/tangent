import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import {
  ApprovalQueue,
  type ApprovalQueueEnvelope,
  type ApprovalQueueResponse,
} from "./components/envelopes/ApprovalQueue";
import {
  BlockDraft,
  type BlockDraftEnvelope,
  type BlockDraftResponse,
} from "./components/envelopes/BlockDraft";
import {
  Dashboard,
  type DashboardEnvelope,
  type DashboardResponse,
} from "./components/envelopes/Dashboard";
import {
  DesignIteration,
  type DesignIterationEnvelope,
  type DesignIterationResponse,
} from "./components/envelopes/DesignIteration";
import {
  DiffReview,
  type DiffReviewEnvelope,
  type DiffReviewResponse,
} from "./components/envelopes/DiffReview";
import {
  Feedback,
  type FeedbackEnvelope,
  type FeedbackResponse,
} from "./components/envelopes/Feedback";
import {
  FilePicker,
  type FilePickerEnvelope,
  type FilePickerResponse,
} from "./components/envelopes/FilePicker";
import {
  FormCollect,
  type FormCollectEnvelope,
  type FormCollectResponse,
} from "./components/envelopes/FormCollect";
import {
  InterviewQuestion,
  type InterviewQuestionEnvelope,
  type InterviewQuestionResponse,
} from "./components/envelopes/InterviewQuestion";
import {
  OutputRender,
  type OutputRenderEnvelope,
  type OutputRenderResponse,
} from "./components/envelopes/OutputRender";
import {
  ProgressPanel,
  type ProgressPanelEnvelope,
  type ProgressPanelResponse,
} from "./components/envelopes/ProgressPanel";
import {
  ProseRevision,
  type ProseRevisionEnvelope,
  type ProseRevisionResponse,
} from "./components/envelopes/ProseRevision";
import {
  SpreadsheetReview,
  type SpreadsheetReviewEnvelope,
  type SpreadsheetReviewResponse,
} from "./components/envelopes/SpreadsheetReview";
import {
  SynthesisNotes,
  type SynthesisNotesEnvelope,
  type SynthesisNotesResponse,
} from "./components/envelopes/SynthesisNotes";
import { Triage, type TriageEnvelope, type TriageResponse } from "./components/envelopes/Triage";
import {
  Whiteboard,
  type WhiteboardEnvelope,
  type WhiteboardSubmitResponse,
} from "./components/envelopes/Whiteboard";
import { Wizard, type WizardEnvelope, type WizardResponse } from "./components/envelopes/Wizard";
import { type EnvelopeComponentProps, register } from "./lib/envelope-registry";
import { seedShellClientIdentity } from "./lib/ws-client";
import "./index.css";

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

function WhiteboardAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <Whiteboard
      envelope={envelope as WhiteboardEnvelope}
      onSubmit={onSubmit as (response: WhiteboardSubmitResponse) => void}
      onCancel={onCancel}
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

register("tangent.triage", TriageAdapter);
register("tangent.approval-queue", ApprovalQueueAdapter);
register("tangent.feedback", FeedbackAdapter);
register("tangent.form-collect", FormCollectAdapter);
register("tangent.design-iteration", DesignIterationAdapter);
register("tangent.interview-question", InterviewQuestionAdapter);
register("tangent.block-draft", BlockDraftAdapter);
register("tangent.prose-revision", ProseRevisionAdapter);
register("tangent.output-render", OutputRenderAdapter);
register("tangent.whiteboard", WhiteboardAdapter);
register("tangent.dashboard", DashboardAdapter);
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

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
