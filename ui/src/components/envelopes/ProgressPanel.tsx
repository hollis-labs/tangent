import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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

export function ProgressPanel({ envelope, onSubmit, onCancel }: ProgressPanelProps) {
  const panelID = envelope.data?.panel_id ?? "";
  const items = envelope.data?.items ?? [];
  const updates = envelope.data?.updates ?? [];
  const checkpoints = envelope.data?.checkpoints ?? [];
  const summary = envelope.data?.summary;
  const [selectedItemID, setSelectedItemID] = useState(items[0]?.item_id ?? "");
  const [status, setStatus] = useState(items[0]?.status ?? "running");
  const [note, setNote] = useState("");
  const [checkpointLabel, setCheckpointLabel] = useState("");
  const [submitError, setSubmitError] = useState<string | null>(null);

  const activeItem = useMemo(
    () => items.find((item) => item.item_id === selectedItemID) ?? items[0] ?? null,
    [items, selectedItemID],
  );

  return (
    <Card data-testid="progress-panel-root">
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? "Progress panel"}</CardTitle>
        <p className="text-sm text-zinc-400">
          Track durable room-backed progress and send explicit status updates through the current
          Tangent session contract.
        </p>
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
                setSubmitError(null);
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
