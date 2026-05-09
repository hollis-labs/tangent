import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import {
  BlockDraft,
  type BlockDraftEnvelope,
  type BlockDraftResponse,
} from "./components/envelopes/BlockDraft";
import {
  DesignIteration,
  type DesignIterationEnvelope,
  type DesignIterationResponse,
} from "./components/envelopes/DesignIteration";
import {
  Feedback,
  type FeedbackEnvelope,
  type FeedbackResponse,
} from "./components/envelopes/Feedback";
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
  ProseRevision,
  type ProseRevisionEnvelope,
  type ProseRevisionResponse,
} from "./components/envelopes/ProseRevision";
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
import { type EnvelopeComponentProps, register } from "./lib/envelope-registry";
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

function FeedbackAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <Feedback
      envelope={envelope as FeedbackEnvelope}
      onSubmit={onSubmit as (response: FeedbackResponse) => void}
      onCancel={onCancel}
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
register("tangent.feedback", FeedbackAdapter);
register("tangent.design-iteration", DesignIterationAdapter);
register("tangent.interview-question", InterviewQuestionAdapter);
register("tangent.block-draft", BlockDraftAdapter);
register("tangent.prose-revision", ProseRevisionAdapter);
register("tangent.output-render", OutputRenderAdapter);
register("tangent.whiteboard", WhiteboardAdapter);
register("tangent.synthesis-notes", SynthesisNotesAdapter);

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("Tangent: #root not found in index.html");
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
