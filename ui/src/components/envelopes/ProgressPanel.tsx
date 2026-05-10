import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  clearProgressPanelDraft,
  loadProgressPanelDraft,
  PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS,
  saveProgressPanelDraft,
} from "@/lib/progress-panel-storage";
import { cn } from "@/lib/utils";

type ProgressItem = {
  item_id: string;
  label: string;
  status: string;
  detail?: string;
  created_at?: string;
  updated_at?: string;
};

type ProgressSummary = {
  current_status?: string;
  headline?: string;
  detail?: string;
  last_update_id?: string;
  last_checkpoint_id?: string;
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
    draft ? "Recovered progress-panel view state from this browser." : null,
  );
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

  return (
    <Card data-testid="progress-panel-root">
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? "Progress panel"}</CardTitle>
        <p className="text-sm text-zinc-400">
          Track durable room-backed progress and send explicit status updates through the current
          Tangent session contract.
        </p>
        {message ? (
          <p data-testid="progress-panel-message" className="text-sm text-emerald-300">
            {message}
          </p>
        ) : null}
      </CardHeader>
      <CardContent className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_360px]">
        <div className="space-y-4">
          <section className="grid gap-3 md:grid-cols-2" data-testid="progress-panel-items">
            {items.map((item) => (
              <button
                key={item.item_id}
                type="button"
                data-testid={`progress-panel-item-${item.item_id}`}
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
              <div className="space-y-1">
                <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                  Inspect history
                </p>
                <p className="text-sm text-zinc-400">
                  Switch between timeline, checkpoint summaries, and structured log metadata.
                </p>
              </div>
              <div className="flex flex-wrap gap-2" data-testid="progress-panel-tabs">
                {[
                  ["timeline", "Timeline"],
                  ["checkpoints", "Checkpoints"],
                  ["logs", "Logs"],
                ].map(([value, label]) => (
                  <button
                    key={value}
                    type="button"
                    data-testid={`progress-panel-tab-${value}`}
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
              <label className="space-y-2 text-sm text-zinc-200">
                <span>Item filter</span>
                <select
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
              <label className="space-y-2 text-sm text-zinc-200">
                <span>Update kind</span>
                <select
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
              <div className="mt-4 space-y-3" data-testid="progress-panel-timeline">
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
              <div className="mt-4 space-y-3" data-testid="progress-panel-checkpoints">
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

          <label className="space-y-2 text-sm text-zinc-200">
            <span>Item</span>
            <select
              value={selectedItemID}
              onChange={(event) => {
                setSelectedItemID(event.target.value);
                const next = items.find((item) => item.item_id === event.target.value);
                if (next) {
                  setStatus(next.status);
                }
              }}
              data-testid="progress-panel-item-select"
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
            >
              {items.map((item) => (
                <option key={item.item_id} value={item.item_id}>
                  {item.label}
                </option>
              ))}
            </select>
          </label>

          <label className="space-y-2 text-sm text-zinc-200">
            <span>Status</span>
            <select
              value={status}
              onChange={(event) => setStatus(event.target.value)}
              data-testid="progress-panel-status-select"
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
            >
              {STATUS_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </label>

          <label className="space-y-2 text-sm text-zinc-200">
            <span>Update note</span>
            <textarea
              value={note}
              onChange={(event) => setNote(event.target.value)}
              data-testid="progress-panel-summary-input"
              className="min-h-28 w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
              placeholder="Summarize what changed or what the operator should know."
            />
          </label>

          <label className="space-y-2 text-sm text-zinc-200">
            <span>Checkpoint label</span>
            <input
              type="text"
              value={checkpointLabel}
              onChange={(event) => setCheckpointLabel(event.target.value)}
              data-testid="progress-panel-checkpoint-input"
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950/80 px-3 py-2"
              placeholder="Optional milestone name"
            />
          </label>

          {submitError ? (
            <p data-testid="progress-panel-submit-error" className="text-sm text-red-400">
              {submitError}
            </p>
          ) : null}

          <div className="flex justify-end gap-3">
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              data-testid="progress-panel-submit"
              onClick={() => {
                if (!panelID || !selectedItemID) {
                  setSubmitError("Select a progress item before submitting an update.");
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
              }}
            >
              Send update
            </Button>
          </div>
        </section>
      </CardContent>
    </Card>
  );
}

function readItemLabel(items: ProgressItem[], itemID: string): string {
  return items.find((item) => item.item_id === itemID)?.label ?? itemID;
}
