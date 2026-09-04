import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import {
  clearProgressPanelDraft,
  loadProgressPanelDraft,
  PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS,
  saveProgressPanelDraft,
} from "@/lib/progress-panel-storage";
import { buildSubmitGate, describedBy, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

type ProgressItem = {
  item_id: string;
  label: string;
  status: string;
  detail?: string;
  created_at?: string;
  updated_at?: string;
  completed_at?: string;
};

type ProgressSummary = {
  current_status?: string;
  headline?: string;
  detail?: string;
  last_update_id?: string;
  last_checkpoint_id?: string;
  last_checkpoint_label?: string;
  completed_at?: string;
  completion_result?: string;
};

type ProgressUpdate = {
  update_id: string;
  kind: "status" | "checkpoint" | "summary";
  item_id?: string;
  status?: string;
  summary?: string;
  created_at?: string;
  checkpoint_id?: string;
  checkpoint_label?: string;
};

export interface ProgressPanelEnvelope {
  v: number;
  id: string;
  type: "tangent.progress-panel";
  title?: string;
  context?: string;
  data?: {
    panel_id: string;
    items: ProgressItem[];
    updates?: ProgressUpdate[];
    checkpoints?: Array<{
      checkpoint_id: string;
      label: string;
      summary?: string;
      created_at?: string;
    }>;
    summary?: ProgressSummary;
    updated_at?: string;
  };
}

export interface ProgressPanelResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    panel_id: string;
    item_id: string;
    status: string;
    summary?: string;
    checkpoint_label?: string;
  };
  completedAt?: string;
}

type ProgressPanelProps = {
  envelope: ProgressPanelEnvelope;
  onSubmit: (response: ProgressPanelResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

const STATUS_OPTIONS = [
  "queued",
  "running",
  "paused",
  "blocked",
  "completed",
  "failed",
  "cancelled",
] as const;

// Control ids. The item select is the one the submit gate points at: it carries
// `item_id`, and the item cards in the left column only reach it indirectly —
// clicking a card two columns away is what sets this field.
const ITEM_SELECT_ID = "progress-panel-item-select";
const STATUS_SELECT_ID = "progress-panel-status-select";
const NOTE_ID = "progress-panel-note";
const CHECKPOINT_ID = "progress-panel-checkpoint";
const FILTER_ITEM_ID = "progress-panel-filter-item";
const FILTER_KIND_ID = "progress-panel-filter-kind";

export function ProgressPanel({ envelope, onSubmit, onCancel, roomID }: ProgressPanelProps) {
  const panelID = envelope.data?.panel_id ?? "";
  const items = envelope.data?.items ?? [];
  const updates = envelope.data?.updates ?? [];
  const checkpoints = envelope.data?.checkpoints ?? [];
  const summary = envelope.data?.summary;
  const [draft] = useState(() =>
    roomID && panelID ? loadProgressPanelDraft(roomID, panelID) : null,
  );
  const [selectedItemID, setSelectedItemID] = useState(
    draft?.selectedItemID || items[0]?.item_id || "",
  );
  const [status, setStatus] = useState(draft?.status || items[0]?.status || "running");
  const [note, setNote] = useState(draft?.note ?? "");
  const [checkpointLabel, setCheckpointLabel] = useState(draft?.checkpointLabel ?? "");
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<"timeline" | "checkpoints" | "logs">(
    draft?.activeTab ?? "timeline",
  );
  const [filterItemID, setFilterItemID] = useState(draft?.filterItemID ?? "");
  const [filterKind, setFilterKind] = useState(draft?.filterKind ?? "all");
  const [selectedUpdateID, setSelectedUpdateID] = useState(draft?.selectedUpdateID ?? "");
  const [message, setMessage] = useState<string | null>(
    draft
      ? "Recovered your unsent progress update — item, status, note, and checkpoint label — along with the timeline filters, from this browser."
      : null,
  );
  const [exportMessage, setExportMessage] = useState<string | null>(null);
  const lastSavedDraftRef = useRef<string | null>(draft ? JSON.stringify(draft) : null);

  const activeItem = useMemo(
    () => items.find((item) => item.item_id === selectedItemID) ?? items[0] ?? null,
    [items, selectedItemID],
  );
  const filteredUpdates = useMemo(
    () =>
      updates.filter((update) => {
        if (filterItemID && update.item_id !== filterItemID) {
          return false;
        }
        if (filterKind !== "all" && update.kind !== filterKind) {
          return false;
        }
        return true;
      }),
    [filterItemID, filterKind, updates],
  );
  const selectedUpdate = useMemo(
    () =>
      filteredUpdates.find((update) => update.update_id === selectedUpdateID) ??
      filteredUpdates[0] ??
      null,
    [filteredUpdates, selectedUpdateID],
  );
  const visibleCheckpoints = useMemo(
    () =>
      checkpoints.filter((checkpoint) => {
        if (!filterItemID) {
          return true;
        }
        const source = updates.find((update) => update.checkpoint_id === checkpoint.checkpoint_id);
        return source?.item_id === filterItemID;
      }),
    [checkpoints, filterItemID, updates],
  );
  const exportPayload = useMemo(
    () =>
      JSON.stringify(
        {
          panel_id: panelID,
          summary,
          items: items.map((item) => ({
            item_id: item.item_id,
            label: item.label,
            status: item.status,
            updated_at: item.updated_at,
            completed_at: item.completed_at,
          })),
          latest_checkpoint: checkpoints.at(-1) ?? null,
          update_count: updates.length,
        },
        null,
        2,
      ),
    [checkpoints, items, panelID, summary, updates.length],
  );

  useEffect(() => {
    if (!roomID || !panelID) {
      return;
    }
    const nextDraft = {
      activeTab,
      filterItemID,
      filterKind,
      selectedUpdateID,
      selectedItemID,
      status,
      note,
      checkpointLabel,
    };
    const serialized = JSON.stringify(nextDraft);
    if (serialized === lastSavedDraftRef.current) {
      return;
    }
    const handle = window.setTimeout(() => {
      saveProgressPanelDraft(roomID, panelID, nextDraft);
      lastSavedDraftRef.current = serialized;
    }, PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(handle);
  }, [
    activeTab,
    checkpointLabel,
    filterItemID,
    filterKind,
    note,
    panelID,
    roomID,
    selectedItemID,
    selectedUpdateID,
    status,
  ]);

  const revealRequirement = useRevealRequirement();

  // The Send update button stays live and validates on click — that click is
  // how this workflow reports "nothing selected", and disabling it would leave
  // an operator with an empty panel and no explanation at all. The gate names
  // the two requirements separately: the old single message claimed an item was
  // unselected even when the real problem was a panel with no id, and even when
  // the panel had no items to select in the first place.
  const gate = buildSubmitGate([
    !panelID && {
      controlID: "",
      label: "panel",
      message: "this envelope carries no panel id, so an update has nowhere to land.",
    },
    !selectedItemID && {
      controlID: items.length === 0 ? "" : ITEM_SELECT_ID,
      label: "the item picker",
      message:
        items.length === 0
          ? "this panel has no progress items to update yet."
          : "no progress item is selected yet.",
    },
  ]);

  // The attempted-submit error is only true while the gate is still blocked;
  // selecting an item clears it without needing a second click.
  const showSubmitError = submitError !== null && gate.blocked;

  function handleSubmit() {
    if (gate.blocked) {
      // Mirrors the old inline guard, but each requirement now states its own
      // truth rather than sharing one wrong sentence.
      setSubmitError(gate.first ? asSentence(gate.first.message) : null);
      revealRequirement(gate.first);
      return;
    }
    setMessage(null);
    setSubmitError(null);
    if (roomID && panelID) {
      clearProgressPanelDraft(roomID, panelID);
      lastSavedDraftRef.current = null;
    }
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        panel_id: panelID,
        item_id: selectedItemID,
        status,
        summary: note.trim() || undefined,
        checkpoint_label: checkpointLabel.trim() || undefined,
      },
    });
  }

  return (
    <Card data-testid="progress-panel-root">
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? "Progress panel"}</CardTitle>
        <p className="text-sm text-zinc-400">
          Track durable room-backed progress and send explicit status updates through the current
          Tangent session contract.
        </p>
        {message ? (
          <p
            data-testid="progress-panel-message"
            role="status"
            aria-live="polite"
            className="text-sm text-emerald-300"
          >
            {message}
          </p>
        ) : null}
      </CardHeader>
      <CardContent className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_360px]">
        <div className="space-y-4">
          <section className="space-y-2" data-testid="progress-panel-items">
            {/*
              These cards are the workflow's most-used control and they mutate
              two fields in the Send update pane on the far right of an
              xl:grid-cols-[…_360px] layout. Nothing on screen said so, which
              made a card click look inert on a wide viewport. The hint names
              the destination and the cards point at it with aria-controls.
            */}
            <p className="text-xs text-zinc-500" data-testid="progress-panel-items-hint">
              Choosing an item here sets Item and Status in the Send update panel.
            </p>
            <div className="grid gap-3 md:grid-cols-2">
              {items.map((item) => (
                <button
                  key={item.item_id}
                  type="button"
                  data-testid={`progress-panel-item-${item.item_id}`}
                  aria-pressed={item.item_id === activeItem?.item_id}
                  aria-controls={ITEM_SELECT_ID}
                  onClick={() => {
                    setSelectedItemID(item.item_id);
                    setStatus(item.status);
                  }}
                  className={cn(
                    "rounded-2xl border p-4 text-left transition",
                    item.item_id === activeItem?.item_id
                      ? "border-emerald-500 bg-emerald-500/10"
                      : "border-zinc-800 bg-zinc-950/60 hover:border-zinc-700",
                  )}
                >
                  <div className="flex items-center justify-between gap-3">
                    <p className="font-medium text-zinc-50">{item.label}</p>
                    <span className="rounded-full border border-zinc-700 px-2 py-1 text-[11px] uppercase tracking-[0.18em] text-zinc-300">
                      {item.status}
                    </span>
                  </div>
                  {item.detail ? <p className="mt-2 text-sm text-zinc-400">{item.detail}</p> : null}
                </button>
              ))}
            </div>
          </section>

          <section className="grid gap-3 md:grid-cols-2">
            <div className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4">
              <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                Summary
              </p>
              <p className="mt-3 text-lg font-semibold text-zinc-50">
                {summary?.headline ?? "No summary yet"}
              </p>
              {summary?.detail ? (
                <p className="mt-2 text-sm text-zinc-400">{summary.detail}</p>
              ) : null}
              <div className="mt-3 flex flex-wrap gap-2 text-xs text-zinc-500">
                {summary?.current_status ? <span>status: {summary.current_status}</span> : null}
                {summary?.last_update_id ? <span>update: {summary.last_update_id}</span> : null}
                {summary?.last_checkpoint_id ? (
                  <span>checkpoint: {summary.last_checkpoint_id}</span>
                ) : null}
                {summary?.last_checkpoint_label ? (
                  <span>label: {summary.last_checkpoint_label}</span>
                ) : null}
                {summary?.completion_result ? (
                  <span>result: {summary.completion_result}</span>
                ) : null}
              </div>
            </div>
            <div className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4">
              <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                Recent activity
              </p>
              <p
                className="mt-3 text-3xl font-semibold text-zinc-50"
                data-testid="progress-panel-update-count"
              >
                {updates.length}
              </p>
              <p className="text-sm text-zinc-400">{checkpoints.length} checkpoints recorded</p>
            </div>
          </section>

          <section className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                  Export snapshot
                </p>
                <p className="text-sm text-zinc-400">
                  Operator-facing summary payload derived from the room-backed progress state.
                </p>
              </div>
              <Button
                type="button"
                variant="secondary"
                data-testid="progress-panel-export-copy"
                onClick={async () => {
                  try {
                    await globalThis.navigator?.clipboard?.writeText(exportPayload);
                    setExportMessage("Copied progress snapshot.");
                  } catch {
                    setExportMessage("Clipboard unavailable; inspect the snapshot below.");
                  }
                }}
              >
                Copy snapshot
              </Button>
            </div>
            {exportMessage ? (
              <p
                data-testid="progress-panel-export-message"
                role="status"
                aria-live="polite"
                className="mt-3 text-sm text-emerald-300"
              >
                {exportMessage}
              </p>
            ) : null}
            <pre
              data-testid="progress-panel-export-payload"
              className="mt-3 min-h-40 whitespace-pre-wrap break-words rounded-xl border border-zinc-800 bg-zinc-950 p-3 text-xs text-zinc-200"
            >
              {exportPayload}
            </pre>
          </section>

          <section className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="space-y-1">
                <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                  Inspect history
                </p>
                <p className="text-sm text-zinc-400">
                  Switch between timeline, checkpoint summaries, and structured log metadata.
                </p>
              </div>
              <div
                className="flex flex-wrap gap-2"
                data-testid="progress-panel-tabs"
                role="tablist"
                aria-label="Inspect history"
              >
                {[
                  ["timeline", "Timeline"],
                  ["checkpoints", "Checkpoints"],
                  ["logs", "Logs"],
                ].map(([value, label]) => (
                  <button
                    key={value}
                    type="button"
                    id={`progress-panel-tab-${value}`}
                    data-testid={`progress-panel-tab-${value}`}
                    role="tab"
                    aria-selected={activeTab === value}
                    aria-controls={`progress-panel-tabpanel-${value}`}
                    onClick={() => setActiveTab(value as "timeline" | "checkpoints" | "logs")}
                    className={cn(
                      "rounded-full border px-3 py-1 text-xs transition",
                      activeTab === value
                        ? "border-emerald-500 bg-emerald-500/10 text-zinc-50"
                        : "border-zinc-700 bg-zinc-950 text-zinc-300",
                    )}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            <div className="mt-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_180px_180px]">
              <label className="space-y-2 text-sm text-zinc-200" htmlFor={FILTER_ITEM_ID}>
                <span className="block">Item filter</span>
                <select
                  id={FILTER_ITEM_ID}
                  value={filterItemID}
                  onChange={(event) => setFilterItemID(event.target.value)}
                  data-testid="progress-panel-filter-item"
                  className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
                >
                  <option value="">All items</option>
                  {items.map((item) => (
                    <option key={item.item_id} value={item.item_id}>
                      {item.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className="space-y-2 text-sm text-zinc-200" htmlFor={FILTER_KIND_ID}>
                <span className="block">Update kind</span>
                <select
                  id={FILTER_KIND_ID}
                  value={filterKind}
                  onChange={(event) => setFilterKind(event.target.value)}
                  data-testid="progress-panel-filter-kind"
                  className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
                >
                  <option value="all">All kinds</option>
                  <option value="status">Status</option>
                  <option value="checkpoint">Checkpoint</option>
                  <option value="summary">Summary</option>
                </select>
              </label>
              <div className="rounded-xl border border-zinc-800 bg-zinc-950/60 px-3 py-2 text-sm text-zinc-400">
                {filteredUpdates.length} visible updates
              </div>
            </div>

            {activeTab === "timeline" ? (
              <div
                className="mt-4 space-y-3"
                data-testid="progress-panel-timeline"
                id="progress-panel-tabpanel-timeline"
                role="tabpanel"
                aria-labelledby="progress-panel-tab-timeline"
              >
                {filteredUpdates.length > 0 ? (
                  filteredUpdates.map((update) => (
                    <button
                      key={update.update_id}
                      type="button"
                      data-testid={`progress-panel-update-${update.update_id}`}
                      onClick={() => setSelectedUpdateID(update.update_id)}
                      className={cn(
                        "w-full rounded-xl border p-3 text-left transition",
                        selectedUpdate?.update_id === update.update_id
                          ? "border-emerald-500 bg-emerald-500/10"
                          : "border-zinc-800 bg-zinc-950/60 hover:border-zinc-700",
                      )}
                    >
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        <div>
                          <p className="font-medium text-zinc-50">
                            {update.item_id
                              ? readItemLabel(items, update.item_id)
                              : "Panel summary"}
                          </p>
                          <p className="text-sm text-zinc-400">
                            {update.summary || update.status || update.kind}
                          </p>
                        </div>
                        <div className="text-right text-xs text-zinc-500">
                          <div>{update.kind}</div>
                          <div>{update.created_at || "pending timestamp"}</div>
                        </div>
                      </div>
                    </button>
                  ))
                ) : (
                  <p className="text-sm text-zinc-500">
                    No timeline entries match the current filter.
                  </p>
                )}
              </div>
            ) : null}

            {activeTab === "checkpoints" ? (
              <div
                className="mt-4 space-y-3"
                data-testid="progress-panel-checkpoints"
                id="progress-panel-tabpanel-checkpoints"
                role="tabpanel"
                aria-labelledby="progress-panel-tab-checkpoints"
              >
                {visibleCheckpoints.length > 0 ? (
                  visibleCheckpoints.map((checkpoint) => (
                    <div
                      key={checkpoint.checkpoint_id}
                      className="rounded-xl border border-zinc-800 bg-zinc-950/60 p-3"
                    >
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        <p className="font-medium text-zinc-50">{checkpoint.label}</p>
                        <span className="text-xs text-zinc-500">
                          {checkpoint.created_at || "pending timestamp"}
                        </span>
                      </div>
                      {checkpoint.summary ? (
                        <p className="mt-2 text-sm text-zinc-400">{checkpoint.summary}</p>
                      ) : null}
                      <p className="mt-2 text-xs text-zinc-500">{checkpoint.checkpoint_id}</p>
                    </div>
                  ))
                ) : (
                  <p className="text-sm text-zinc-500">No checkpoints match the current filter.</p>
                )}
              </div>
            ) : null}

            {activeTab === "logs" ? (
              <div
                className="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]"
                data-testid="progress-panel-logs"
                id="progress-panel-tabpanel-logs"
                role="tabpanel"
                aria-labelledby="progress-panel-tab-logs"
              >
                <div className="space-y-2">
                  {filteredUpdates.length > 0 ? (
                    filteredUpdates.map((update) => (
                      <button
                        key={update.update_id}
                        type="button"
                        data-testid={`progress-panel-log-${update.update_id}`}
                        onClick={() => setSelectedUpdateID(update.update_id)}
                        className={cn(
                          "w-full rounded-xl border px-3 py-2 text-left text-sm transition",
                          selectedUpdate?.update_id === update.update_id
                            ? "border-emerald-500 bg-emerald-500/10 text-zinc-50"
                            : "border-zinc-800 bg-zinc-950/60 text-zinc-300 hover:border-zinc-700",
                        )}
                      >
                        <div className="font-medium">{update.update_id}</div>
                        <div className="text-xs text-zinc-500">
                          {update.kind} · {update.item_id || "panel"}
                        </div>
                      </button>
                    ))
                  ) : (
                    <p className="text-sm text-zinc-500">
                      No log entries match the current filter.
                    </p>
                  )}
                </div>
                <pre
                  data-testid="progress-panel-log-detail"
                  className="min-h-52 whitespace-pre-wrap break-words rounded-xl border border-zinc-800 bg-zinc-950 p-3 text-xs text-zinc-200"
                >
                  {selectedUpdate
                    ? JSON.stringify(selectedUpdate, null, 2)
                    : "Select an update to inspect its structured payload."}
                </pre>
              </div>
            ) : null}
          </section>
        </div>

        <section className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4">
          <div className="space-y-1">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
              Send update
            </p>
            <p className="text-sm text-zinc-400">
              Select one item, set its next status, and optionally capture a checkpoint label.
            </p>
          </div>

          <div className="grid gap-2 sm:grid-cols-3" data-testid="progress-panel-controls">
            <Button
              type="button"
              variant="secondary"
              data-testid="progress-panel-control-pause"
              onClick={() => {
                if (!activeItem) {
                  return;
                }
                setSelectedItemID(activeItem.item_id);
                setStatus("paused");
                setNote(`Paused ${activeItem.label.toLowerCase()}.`);
              }}
            >
              Pause
            </Button>
            <Button
              type="button"
              variant="secondary"
              data-testid="progress-panel-control-resume"
              onClick={() => {
                if (!activeItem) {
                  return;
                }
                setSelectedItemID(activeItem.item_id);
                setStatus("running");
                setNote(`Resumed ${activeItem.label.toLowerCase()}.`);
              }}
            >
              Resume
            </Button>
            <Button
              type="button"
              variant="secondary"
              data-testid="progress-panel-control-cancel"
              onClick={() => {
                if (!activeItem) {
                  return;
                }
                setSelectedItemID(activeItem.item_id);
                setStatus("cancelled");
                setNote(`Cancelled ${activeItem.label.toLowerCase()}.`);
              }}
            >
              Cancel
            </Button>
          </div>
          {/*
            These three write item, status *and* note in one click, with no
            confirmation and no undo, and they sit directly above the note they
            overwrite. Saying so is the minimum; making them non-destructive is
            a separate change to the workflow's behaviour.
          */}
          <p className="text-xs text-zinc-500" data-testid="progress-panel-controls-hint">
            Each quick action sets the item and status and replaces anything typed in Update note.
          </p>

          <div className="space-y-2">
            <label className="block text-sm text-zinc-200" htmlFor={ITEM_SELECT_ID}>
              Item
              <RequiredMark active={!selectedItemID} testID="progress-panel-item-required" />
            </label>
            <select
              id={ITEM_SELECT_ID}
              value={selectedItemID}
              onChange={(event) => {
                setSelectedItemID(event.target.value);
                const next = items.find((item) => item.item_id === event.target.value);
                if (next) {
                  setStatus(next.status);
                }
              }}
              data-testid="progress-panel-item-select"
              aria-required={!selectedItemID}
              aria-invalid={!selectedItemID}
              aria-describedby={describedBy(
                `${ITEM_SELECT_ID}-hint`,
                showSubmitError && `${ITEM_SELECT_ID}-error`,
              )}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
            >
              {items.map((item) => (
                <option key={item.item_id} value={item.item_id}>
                  {item.label}
                </option>
              ))}
            </select>
            <FieldMessage id={`${ITEM_SELECT_ID}-hint`}>
              The item this update is recorded against. Also set by the item cards on the left.
            </FieldMessage>
            {showSubmitError ? (
              <FieldMessage
                id={`${ITEM_SELECT_ID}-error`}
                tone="error"
                testID="progress-panel-submit-error"
              >
                {submitError}
              </FieldMessage>
            ) : null}
          </div>

          <div className="space-y-2">
            <label className="block text-sm text-zinc-200" htmlFor={STATUS_SELECT_ID}>
              Status
            </label>
            <select
              id={STATUS_SELECT_ID}
              value={status}
              onChange={(event) => setStatus(event.target.value)}
              data-testid="progress-panel-status-select"
              aria-describedby={`${STATUS_SELECT_ID}-hint`}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
            >
              {STATUS_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
            <FieldMessage id={`${STATUS_SELECT_ID}-hint`}>
              The status the selected item moves to. Defaults to the item's current status.
            </FieldMessage>
          </div>

          {/*
            "Update note" is optional in the payload (`summary: note.trim() ||
            undefined`) but it is also the only place a blocked, cancelled or
            failed status can carry its reason — the placeholder used to read as
            a mandate and said neither of those things. The label keeps the
            workflow's wording; the hint reconciles it with the `summary` key
            the payload actually uses.
          */}
          <div className="space-y-2">
            <label className="block text-sm text-zinc-200" htmlFor={NOTE_ID}>
              Update note
            </label>
            <textarea
              id={NOTE_ID}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              data-testid="progress-panel-summary-input"
              className="min-h-28 w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
              placeholder="Optional — what changed, or why"
              aria-describedby={`${NOTE_ID}-hint`}
            />
            <FieldMessage id={`${NOTE_ID}-hint`}>
              Optional. Sent as the update's summary, and the only place a paused, blocked,
              cancelled or failed status records why. The quick actions above overwrite it.
            </FieldMessage>
          </div>

          <div className="space-y-2">
            <label className="block text-sm text-zinc-200" htmlFor={CHECKPOINT_ID}>
              Checkpoint label
            </label>
            <input
              id={CHECKPOINT_ID}
              type="text"
              value={checkpointLabel}
              onChange={(event) => setCheckpointLabel(event.target.value)}
              data-testid="progress-panel-checkpoint-input"
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
              placeholder="Optional milestone name"
              aria-describedby={`${CHECKPOINT_ID}-hint`}
            />
            <FieldMessage id={`${CHECKPOINT_ID}-hint`}>
              Optional. Names a milestone alongside this update.
            </FieldMessage>
          </div>

          <div className="flex flex-wrap items-center justify-end gap-3">
            <SubmitGateNotice
              gate={gate}
              testID="progress-panel-submit-gate"
              action="Send update"
              mode="attempt"
              onReveal={revealRequirement}
            />
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              data-testid="progress-panel-submit"
              onClick={handleSubmit}
              aria-describedby={gate.blocked ? "progress-panel-submit-gate" : undefined}
            >
              Send update
            </Button>
          </div>
        </section>
      </CardContent>
    </Card>
  );
}

/**
 * Render a gate requirement as a standalone sentence.
 *
 * Gate messages are lowercase because the notice prefixes them ("Cannot send
 * update yet: …"); the field-level error has no prefix and needs the capital.
 */
function asSentence(message: string): string {
  return message.charAt(0).toUpperCase() + message.slice(1);
}

function readItemLabel(items: ProgressItem[], itemID: string): string {
  return items.find((item) => item.item_id === itemID)?.label ?? itemID;
}
