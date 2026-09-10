// Room is the per-session view rendered at /r/:roomID. Connects to the
// Tangent /ws endpoint and delegates rendering to EnvelopeRouter.
//
// PR 5 changes from PR 4:
//   - Mock submit/cancel buttons removed; the registered envelope
//     component owns its own UX (e.g. <Triage> renders Submit/Cancel
//     buttons that call back through onSubmit/onCancel).
//   - The Room only owns the WS transport; envelope-shape decisions
//     live in the component layer.
//
// Two lifecycles, rendered separately. `pending` is the interaction this tab
// is looking at; `connection`/`sync` are who is attached to the surface and
// which durable revisions this tab holds. Neither implies the other, so
// ConnectionStatus is drawn whether or not there is an envelope, and the
// envelope pane says nothing about sockets.
//
// Lifecycle:
//   1. On mount, open WSClient(roomID).
//   2. On `onEnvelope`, store the envelope id + payload in state.
//   3. EnvelopeRouter dispatches by type and fires onSubmit/onCancel.
//   4. On unmount or onClose, close the WS.
//
// `pending` is the presentation whose renderer is *mounted*, which is not the
// same thing as the answer the server is still waiting for — `submitting` says
// that. Keeping the two apart is the whole of CW-20260905-0016: the optimistic
// clear used to be `setPending(null)`, which unmounted the workflow component
// and destroyed every answer the operator had typed, because that is where the
// answers live. The lifecycle then restored the envelope on a refusal and the
// operator got a blank form back.

import { useEffect, useEffectEvent, useRef, useState } from "react";
import { useParams } from "react-router-dom";

import { ConnectionStatus } from "../components/ConnectionStatus";
import { EnvelopeRouter } from "../components/envelopes/EnvelopeRouter";
import { createRoomLifecycle, type RoomLifecycle } from "../lib/room-lifecycle";
import { fetchRoomState } from "../lib/rooms-api";
import {
  type ConnectionState,
  connect,
  type ServerError,
  type SurfaceSync,
} from "../lib/ws-client";

type Pending = {
  envelopeId: string;
  envelope: unknown;
  revision: number;
  roomID: string;
};

export default function Room() {
  const { roomID } = useParams<{ roomID: string }>();
  const [pending, setPending] = useState<Pending | null>(null);
  // True from the moment a response or cancel goes out until the server either
  // presents something new or refuses it. The renderer stays mounted for the
  // whole of it; this only decides whether the operator can see and touch it.
  const [submitting, setSubmitting] = useState(false);
  const [status, setStatus] = useState<string>("waiting for envelope...");
  const [transport, setTransport] = useState<string>("connecting...");
  const [connection, setConnection] = useState<ConnectionState | null>(null);
  const [sync, setSync] = useState<SurfaceSync | null>(null);
  const [serverError, setServerError] = useState<ServerError | null>(null);
  const [error, setError] = useState<string | null>(null);
  const lifecycleRef = useRef<RoomLifecycle | null>(null);
  const initialRoomRef = useRef<string | null>(roomID ?? null);
  const handleEnvelope = useEffectEvent(
    async (envelopeId: string, envelope: unknown, revision = 0) => {
      const lifecycle = lifecycleRef.current;
      lifecycle?.receiveEnvelope(envelopeId, envelope, revision);
      const targetRoomID = lifecycle?.currentRoomID();
      const enriched = targetRoomID ? await enrichEnvelope(targetRoomID, envelope) : envelope;

      // Enrichment is asynchronous for stateful workflows. A route switch or
      // a newer replay may win while it is in flight; only render if this is
      // still the exact active presentation.
      const currentLifecycle = lifecycleRef.current;
      const active = currentLifecycle?.activeEnvelope();
      if (
        !targetRoomID ||
        !currentLifecycle ||
        currentLifecycle !== lifecycle ||
        currentLifecycle.currentRoomID() !== targetRoomID ||
        active?.envelopeId !== envelopeId ||
        active.revision !== revision
      ) {
        return;
      }
      setPending({ envelopeId, envelope: enriched, revision, roomID: targetRoomID });
      setSubmitting(false);
      setStatus("envelope received");
      setServerError(null);
    },
  );

  useEffect(() => {
    if (!initialRoomRef.current) {
      setError("missing roomID in URL");
      return;
    }
    const client = connect(initialRoomRef.current, {
      onOpen: () => {
        setTransport("connected");
      },
      onEnvelope: handleEnvelope,
      onConnectionState: (state) => {
        lifecycleRef.current?.receiveConnectionState(state);
        setConnection(state);
        setServerError(lifecycleRef.current?.lastServerError() ?? null);
      },
      onSync: (next) => {
        lifecycleRef.current?.receiveSync(next);
        setSync(next);
      },
      onServerError: (refused) => {
        // A refused action did not happen, so the operator goes back to the
        // form they were looking at — the same component instance, still
        // holding everything they typed, not a fresh one seeded from
        // `envelope.data`. The lifecycle restore stays as the backstop for the
        // paths that genuinely have no mounted renderer left.
        lifecycleRef.current?.receiveServerError(refused);
        setServerError(refused);
        setPending((current) => current ?? restoredPending(lifecycleRef.current));
        setSubmitting(false);
        setStatus("submission refused");
      },
      onClose: (reason) => {
        // Losing the socket is a connection fact only: the envelope stays
        // presented server-side, and this tab clears its local view because it
        // can no longer be sure the view is current.
        lifecycleRef.current?.clearEnvelope();
        setTransport(`disconnected: ${reason}`);
        setConnection(null);
        setPending(null);
        setSubmitting(false);
      },
      onError: (err) => {
        setError(err.message);
      },
    });
    const lifecycle = createRoomLifecycle(initialRoomRef.current, client);
    lifecycleRef.current = lifecycle;
    return () => {
      lifecycle.dispose();
      lifecycleRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (!roomID || !lifecycleRef.current) {
      return;
    }
    if (!lifecycleRef.current.switchRoom(roomID)) {
      return;
    }
    setPending(null);
    setSubmitting(false);
    setConnection(null);
    setSync(null);
    setServerError(null);
    setTransport("switching rooms...");
    setStatus("waiting for envelope...");
  }, [roomID]);

  const handleSubmit = (response: unknown) => {
    if (!lifecycleRef.current?.submit(response)) return;
    setSubmitting(true);
    setStatus("response submitted");
  };

  const handleCancel = () => {
    if (!lifecycleRef.current?.cancel()) return;
    setSubmitting(true);
    setStatus("cancelled");
  };

  // A draft is not a submission. Nothing here sets `submitting`, nothing clears
  // `pending`, and no status line claims an outcome — the envelope is still
  // open and the participant is still in it. Reporting a draft the way a
  // response is reported would tell the operator they had answered when they
  // had only looked.
  const handleDraft = (draft: unknown) => {
    lifecycleRef.current?.saveDraft(draft);
  };

  return (
    <main className="min-h-screen bg-zinc-950 text-zinc-100 p-6">
      <header className="mb-4 space-y-2">
        <h1 className="text-lg font-medium">Room {roomID}</h1>
        <ConnectionStatus
          transport={transport}
          connection={connection}
          sync={sync}
          serverError={serverError}
          onTakeOver={() => lifecycleRef.current?.claimResolver(true)}
          onRelease={() => lifecycleRef.current?.releaseResolver()}
          onResync={() => lifecycleRef.current?.resync()}
        />
        <p className="text-xs text-zinc-400">status: {status}</p>
        {error ? <p className="text-xs text-red-400">error: {error}</p> : null}
      </header>

      {pending ? (
        // Hidden while a submission is outstanding, never unmounted. The pane
        // reads exactly as it did before — an accepted answer leaves "waiting
        // for envelope..." behind, with nothing stale on screen — but the
        // component behind it keeps its state, so a refusal is a re-reveal of
        // the operator's own filled-in form rather than a rebuild from
        // `envelope.data`. `inert` makes "not on screen" also mean "not
        // reachable", by keyboard or by an assistive technology.
        <section className="space-y-3" hidden={submitting} inert={submitting}>
          <div className="text-xs text-zinc-500">envelope: {pending.envelopeId}</div>
          <EnvelopeRouter
            // Presentation identity, and now the only thing that resets a
            // workflow's local state. Most renderers seed `useState` from
            // `envelope.data` once and never re-sync when the prop changes —
            // safe only while the Room remounted between envelopes, which it no
            // longer does. Keying on the envelope id keeps that guarantee
            // without the unmount: a different envelope is a different
            // component, a replay of the same one is not. So the operator's
            // answers survive a refusal and can never leak into the next
            // envelope (CW-20260904-0141 item 5).
            key={pending.envelopeId}
            envelope={pending.envelope}
            onSubmit={handleSubmit}
            onCancel={handleCancel}
            roomID={roomID}
            onDraft={handleDraft}
          />
        </section>
      ) : null}
      {!pending || submitting ? (
        <p className="text-sm text-zinc-500">waiting for envelope...</p>
      ) : null}
    </main>
  );
}

// restoredPending re-derives the render state from the presentation the
// lifecycle held back. Enrichment is not re-run: the envelope was already
// enriched when it was first presented.
function restoredPending(lifecycle: RoomLifecycle | null): Pending | null {
  const active = lifecycle?.activeEnvelope();
  if (!lifecycle || !active) {
    return null;
  }
  return {
    envelopeId: active.envelopeId,
    envelope: active.envelope,
    revision: active.revision,
    roomID: lifecycle.currentRoomID(),
  };
}

async function enrichEnvelope(roomID: string, envelope: unknown): Promise<unknown> {
  const type = readEnvelopeType(envelope);
  if (
    type !== "tangent.design-iteration" &&
    type !== "tangent.synthesis-notes" &&
    type !== "tangent.block-draft" &&
    type !== "tangent.prose-revision" &&
    type !== "tangent.output-render" &&
    type !== "tangent.dashboard" &&
    type !== "tangent.approval-queue" &&
    type !== "tangent.form-collect" &&
    type !== "tangent.wizard" &&
    type !== "tangent.file-picker" &&
    type !== "tangent.progress-panel" &&
    type !== "tangent.diff-review" &&
    type !== "tangent.whiteboard" &&
    type !== "tangent.spreadsheet-review"
  ) {
    return envelope;
  }
  try {
    const state = await fetchRoomState(roomID);
    if (type === "tangent.design-iteration") {
      return attachPriorVariants(envelope, state.envelopes_history ?? []);
    }
    if (type === "tangent.block-draft") {
      return attachCurrentDraft(envelope, state.current_draft);
    }
    if (type === "tangent.prose-revision") {
      return attachCurrentDraft(envelope, state.current_draft);
    }
    if (type === "tangent.output-render") {
      return attachFinalOutput(envelope, state.final_output);
    }
    if (type === "tangent.form-collect") {
      return attachFormCollectState(envelope, state.form_collect);
    }
    if (type === "tangent.wizard") {
      return attachWizardState(envelope, state.wizard);
    }
    if (type === "tangent.dashboard") {
      return attachDashboardState(envelope, state.dashboard);
    }
    if (type === "tangent.file-picker") {
      return attachFilePickerState(envelope, state.file_picker);
    }
    if (type === "tangent.progress-panel") {
      return attachProgressPanelState(envelope, state.progress_panel);
    }
    if (type === "tangent.diff-review") {
      return attachDiffReviewState(envelope, state.diff_review);
    }
    if (type === "tangent.approval-queue") {
      return attachApprovalQueueState(envelope, state.approval_queue);
    }
    if (type === "tangent.spreadsheet-review") {
      return attachSpreadsheetReviewState(envelope, state.spreadsheet_review);
    }
    if (type === "tangent.whiteboard") {
      return attachWhiteboardState(envelope, state.whiteboard);
    }
    return attachSynthesisState(envelope, state.synthesis_notes);
  } catch {
    return envelope;
  }
}

function readEnvelopeType(envelope: unknown): string | null {
  if (!envelope || typeof envelope !== "object") {
    return null;
  }
  const type = (envelope as { type?: unknown }).type;
  return typeof type === "string" ? type : null;
}

function attachPriorVariants(envelope: unknown, history: unknown[]): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const priorVariants = history
    .map((item) => {
      if (!item || typeof item !== "object") {
        return null;
      }
      const record = item as Record<string, unknown>;
      if (record.type !== "tangent.design-iteration") {
        return null;
      }
      const req = record.envelope;
      if (!req || typeof req !== "object") {
        return null;
      }
      const env = req as Record<string, unknown>;
      const envData =
        env.data && typeof env.data === "object" ? (env.data as Record<string, unknown>) : {};
      const variantID = envData.variant_id;
      const html = envData.html;
      const envelopeID = env.id;
      if (
        typeof variantID !== "string" ||
        typeof html !== "string" ||
        typeof envelopeID !== "string"
      ) {
        return null;
      }
      return {
        envelope_id: envelopeID,
        variant_id: variantID,
        title: typeof env.title === "string" ? env.title : undefined,
        caption: typeof envData.caption === "string" ? envData.caption : undefined,
        html,
      };
    })
    .filter((item): item is NonNullable<typeof item> => item !== null);

  data.prior_variants = priorVariants;
  return { ...typed, data };
}

function attachSpreadsheetReviewState(envelope: unknown, spreadsheetReview: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!spreadsheetReview || typeof spreadsheetReview !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = spreadsheetReview as Record<string, unknown>;
  for (const key of [
    "table_id",
    "columns",
    "rows",
    "query_state",
    "notes",
    "updated_at",
    "saved_views",
    "row_actions",
    "selected_row_ids",
    "selected_rows",
    "action_id",
    "export_refs",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachDashboardState(envelope: unknown, dashboard: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!dashboard || typeof dashboard !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = dashboard as Record<string, unknown>;
  for (const key of [
    "dashboard_id",
    "title",
    "tiles",
    "layout",
    "saved_layouts",
    "active_layout_id",
    "query_state",
    "summary",
    "snapshot_history",
    "export_state",
    "updated_at",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachFilePickerState(envelope: unknown, filePicker: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!filePicker || typeof filePicker !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = filePicker as Record<string, unknown>;
  for (const key of [
    "picker_id",
    "browse_roots",
    "selected_refs",
    "query_state",
    "selection_revisions",
    "updated_at",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachProgressPanelState(envelope: unknown, progressPanel: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!progressPanel || typeof progressPanel !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = progressPanel as Record<string, unknown>;
  for (const key of ["panel_id", "items", "updates", "checkpoints", "summary", "updated_at"]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachWizardState(envelope: unknown, wizard: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!wizard || typeof wizard !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = wizard as Record<string, unknown>;
  for (const key of [
    "wizard_id",
    "title",
    "description",
    "steps",
    "current_step_id",
    "progress",
    "branch_selections",
    "summary",
    "updated_at",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachDiffReviewState(envelope: unknown, diffReview: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!diffReview || typeof diffReview !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = diffReview as Record<string, unknown>;
  for (const key of [
    "review_id",
    "files",
    "current_file",
    "filter_state",
    "decisions",
    "comments",
    "updated_at",
    "summary",
    "before_ref",
    "after_ref",
    "export_refs",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachFormCollectState(envelope: unknown, formCollect: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  if (!formCollect || typeof formCollect !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const persisted = formCollect as Record<string, unknown>;
  for (const key of [
    "form_id",
    "intent",
    "schema",
    "answers",
    "notes",
    "updated_at",
    "saved_drafts",
    "templates",
    "actions",
    "attachment_refs",
    "submission_summary",
  ]) {
    if (persisted[key] !== undefined) {
      data[key] = persisted[key];
    }
  }
  return { ...typed, data };
}

function attachSynthesisState(envelope: unknown, synthesisNotes: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  if (synthesisNotes && typeof synthesisNotes === "object") {
    Object.assign(data, synthesisNotes as Record<string, unknown>);
  }
  return { ...typed, data };
}

function attachCurrentDraft(envelope: unknown, currentDraft: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  if (currentDraft && typeof currentDraft === "object") {
    data.current_draft = currentDraft;
  }
  return { ...typed, data };
}

function attachFinalOutput(envelope: unknown, finalOutput: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  if (finalOutput && typeof finalOutput === "object") {
    Object.assign(data, finalOutput as Record<string, unknown>);
  }
  return { ...typed, data };
}

function attachWhiteboardState(envelope: unknown, whiteboard: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  if (whiteboard && typeof whiteboard === "object") {
    const persisted = whiteboard as Record<string, unknown>;
    data.board_id = persisted.board_id ?? data.board_id;
    data.scene = persisted.scene_snapshot ?? data.scene;
    data.assets = persisted.assets ?? data.assets;
    data.reference_images = Array.isArray(persisted.assets)
      ? (persisted.assets as Array<Record<string, unknown>>).filter(
          (asset) => asset?.kind === "reference_image",
        )
      : data.reference_images;
    data.export_refs = persisted.export_refs ?? data.export_refs;
    data.notes = persisted.notes ?? data.notes;
    data.updated_at = persisted.updated_at ?? data.updated_at;
    data.revision_history = persisted.revision_history ?? data.revision_history;
    if (Array.isArray(persisted.revision_history) && persisted.revision_history.length > 0) {
      const current = persisted.revision_history[persisted.revision_history.length - 1];
      if (current && typeof current === "object") {
        data.revision_id = (current as { revision_id?: unknown }).revision_id ?? data.revision_id;
      }
    }
  }
  return { ...typed, data };
}

function attachApprovalQueueState(envelope: unknown, approvalQueue: unknown): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  if (approvalQueue && typeof approvalQueue === "object") {
    const persisted = approvalQueue as Record<string, unknown>;
    if (typeof persisted.queue_id === "string") {
      data.queue_id = persisted.queue_id;
    }
    if (Array.isArray(persisted.items)) {
      data.items = persisted.items;
    }
    if (typeof persisted.current_index === "number") {
      data.current_index = persisted.current_index;
    }
    if (Array.isArray(persisted.decisions)) {
      data.decisions = persisted.decisions;
    }
    if (typeof persisted.notes === "string") {
      data.notes = persisted.notes;
    }
    if (Array.isArray(persisted.audit_trail)) {
      data.audit_trail = persisted.audit_trail;
    }
    if (Array.isArray(persisted.export_refs)) {
      data.export_refs = persisted.export_refs;
    }
    if (typeof persisted.updated_at === "string") {
      data.updated_at = persisted.updated_at;
    }
  }
  return { ...typed, data };
}
