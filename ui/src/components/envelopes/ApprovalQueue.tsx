import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  APPROVAL_QUEUE_AUTOSAVE_DEBOUNCE_MS,
  type ApprovalQueueDraftDecision,
  buildApprovalQueueCanonicalSeedKey,
  clearApprovalQueueDraft,
  loadApprovalQueueDraft,
  saveApprovalQueueDraft,
} from "@/lib/approval-queue-draft-storage";
import { cn } from "@/lib/utils";

type ApprovalDecision = "accept" | "reject" | "defer";

type EvidenceItem = {
  id: string;
  label: string;
  kind: string;
  content: string;
  uri?: string;
};

type ActionOption = {
  id: string;
  label: string;
};

type ApprovalItem = {
  id: string;
  title: string;
  summary: string;
  description: string;
  evidence: EvidenceItem[];
  actionOptions: ActionOption[];
};

type PersistedDecision = {
  item_id: string;
  decision: ApprovalDecision;
  comment?: string;
  action_id?: string;
  defer_reason?: string;
  decided_at?: string;
};

type ExportRef = {
  name: string;
  created_at?: string;
  item_count?: number;
  decision_count?: number;
  size_bytes?: number;
};

type AuditEntry = {
  item_id?: string;
  event: string;
  decision?: string;
  comment?: string;
  action_id?: string;
  defer_reason?: string;
  at?: string;
};

export interface ApprovalQueueEnvelope {
  v: number;
  id: string;
  type: "tangent.approval-queue";
  title?: string;
  context?: string;
  data?: {
    queue_id: string;
    title?: string;
    intent?: string;
    items?: Array<Record<string, unknown>>;
    current_index?: number;
    decisions?: PersistedDecision[];
    notes?: string;
    updated_at?: string;
    audit_trail?: AuditEntry[];
    export_refs?: ExportRef[];
  };
}

export interface ApprovalQueueResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    queue_id: string;
    current_index: number;
    decisions: PersistedDecision[];
    notes: string;
    export_refs: ExportRef[];
  };
  completedAt?: string;
}

type DecisionState = {
  decision?: ApprovalDecision;
  comment: string;
  action_id: string;
  defer_reason: string;
  decided_at?: string;
};

export type ApprovalQueueProps = {
  envelope: ApprovalQueueEnvelope;
  onSubmit: (response: ApprovalQueueResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function ApprovalQueue({ envelope, onSubmit, onCancel, roomID }: ApprovalQueueProps) {
  const queueID = envelope.data?.queue_id ?? "";
  const items = useMemo(() => normalizeItems(envelope.data?.items), [envelope.data?.items]);
  const canonicalNotes = envelope.data?.notes ?? "";
  const baseSeedKey = useMemo(
    () =>
      buildApprovalQueueCanonicalSeedKey({
        queueID,
        items,
        notes: canonicalNotes,
      }),
    [queueID, items, canonicalNotes],
  );
  const draft = roomID && queueID ? loadApprovalQueueDraft(roomID, queueID) : null;
  const recoveredDraft =
    draft && (draft.envelopeId === envelope.id || draft.baseSeedKey === baseSeedKey) ? draft : null;
  const persistedDecisions = useMemo(
    () => buildDecisionMap(envelope.data?.decisions),
    [envelope.data?.decisions],
  );

  const [currentIndex, setCurrentIndex] = useState(() =>
    clampIndex(recoveredDraft?.currentIndex ?? envelope.data?.current_index ?? 0, items.length),
  );
  const [decisions, setDecisions] = useState<Record<string, DecisionState>>(() =>
    recoveredDraft ? buildDecisionMap(recoveredDraft.decisions) : persistedDecisions,
  );
  const [notes, setNotes] = useState(recoveredDraft?.notes ?? canonicalNotes);
  const [exportRefs, setExportRefs] = useState<ExportRef[]>(
    recoveredDraft
      ? normalizeExportRefs(recoveredDraft.exportRefs)
      : normalizeExportRefs(envelope.data?.export_refs),
  );
  const [message, setMessage] = useState<string | null>(
    recoveredDraft ? "Recovered unsent approval-queue state from this browser." : null,
  );
  const [batchDecision, setBatchDecision] = useState<ApprovalDecision>("accept");
  const [batchDeferReason, setBatchDeferReason] = useState("");
  const [evidenceIndex, setEvidenceIndex] = useState(0);
  const draftJsonRef = useRef<string | null>(null);

  const currentItem = items[currentIndex] ?? null;
  const currentDecision = currentItem ? decisions[currentItem.id] : undefined;

  useEffect(() => {
    if (!roomID || !queueID) {
      return;
    }
    const timerID = window.setTimeout(() => {
      const orderedDecisions = buildOrderedDecisionArray(items, decisions);
      const next = JSON.stringify({
        currentIndex,
        decisions: orderedDecisions,
        notes,
        exportRefs,
      });
      if (next === draftJsonRef.current) {
        return;
      }
      const canonical = JSON.stringify({
        currentIndex: clampIndex(envelope.data?.current_index ?? 0, items.length),
        decisions: buildOrderedDecisionArray(items, persistedDecisions),
        notes: canonicalNotes,
        exportRefs: normalizeExportRefs(envelope.data?.export_refs),
      });
      if (next === canonical) {
        clearApprovalQueueDraft(roomID, queueID);
        draftJsonRef.current = null;
        return;
      }
      saveApprovalQueueDraft({
        version: 1,
        roomID,
        queueID,
        envelopeId: envelope.id,
        baseSeedKey,
        currentIndex,
        decisions: orderedDecisions as ApprovalQueueDraftDecision[],
        notes,
        exportRefs,
        savedAt: new Date().toISOString(),
      });
      draftJsonRef.current = next;
    }, APPROVAL_QUEUE_AUTOSAVE_DEBOUNCE_MS);

    return () => window.clearTimeout(timerID);
  }, [
    roomID,
    queueID,
    envelope.id,
    baseSeedKey,
    currentIndex,
    items,
    decisions,
    notes,
    exportRefs,
    envelope.data?.current_index,
    envelope.data?.export_refs,
    persistedDecisions,
    canonicalNotes,
  ]);

  const applyDecisionEvent = useEffectEvent((itemID: string, decision: ApprovalDecision) => {
    setDecisions((current) => {
      const existing = current[itemID] ?? { comment: "", action_id: "", defer_reason: "" };
      return {
        ...current,
        [itemID]: {
          ...existing,
          decision,
          decided_at: new Date().toISOString(),
        },
      };
    });
  });

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const tagName = target?.tagName?.toLowerCase();
      if (event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }
      if (tagName === "input" || tagName === "textarea" || tagName === "select") {
        return;
      }
      if (!currentItem) {
        return;
      }
      if (event.key === "ArrowLeft" || event.key === "k") {
        event.preventDefault();
        setCurrentIndex((value) => {
          setEvidenceIndex(0);
          return clampIndex(value - 1, items.length);
        });
      } else if (event.key === "ArrowRight" || event.key === "j") {
        event.preventDefault();
        setCurrentIndex((value) => {
          setEvidenceIndex(0);
          return clampIndex(value + 1, items.length);
        });
      } else if (event.key === "1") {
        event.preventDefault();
        applyDecisionEvent(currentItem.id, "accept");
      } else if (event.key === "2") {
        event.preventDefault();
        applyDecisionEvent(currentItem.id, "reject");
      } else if (event.key === "3") {
        event.preventDefault();
        applyDecisionEvent(currentItem.id, "defer");
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [currentItem, items.length]);

  const unresolvedCount = items.filter((item) => !decisions[item.id]?.decision).length;
  const hasInvalidDefer = items.some((item) => {
    const decision = decisions[item.id];
    return decision?.decision === "defer" && decision.defer_reason.trim().length === 0;
  });
  const submitDisabled = items.length === 0 || unresolvedCount > 0 || hasInvalidDefer;

  function updateCurrentDecision(patch: Partial<DecisionState>) {
    if (!currentItem) {
      return;
    }
    setDecisions((current) => ({
      ...current,
      [currentItem.id]: {
        ...(current[currentItem.id] ?? {
          comment: "",
          action_id: "",
          defer_reason: "",
        }),
        ...patch,
      },
    }));
  }

  function applyBatchDecision() {
    setDecisions((current) => {
      const next = { ...current };
      for (const item of items) {
        if (next[item.id]?.decision) {
          continue;
        }
        next[item.id] = {
          comment: next[item.id]?.comment ?? "",
          action_id: next[item.id]?.action_id ?? "",
          defer_reason: batchDecision === "defer" ? batchDeferReason : "",
          decision: batchDecision,
          decided_at: new Date().toISOString(),
        };
      }
      return next;
    });
  }

  function handleExportAudit() {
    const exportedAt = new Date().toISOString();
    const payload = {
      queue_id: queueID,
      exported_at: exportedAt,
      decisions: buildOrderedDecisionArray(items, decisions),
      notes,
      audit_trail: envelope.data?.audit_trail ?? [],
    };
    const serialized = JSON.stringify(payload, null, 2);
    const exportBlob = new Blob([serialized], { type: "application/json" });
    const exportName = `${sanitizeFilePart(queueID || "approval-queue")}-audit-${exportedAt.replaceAll(/[:.]/g, "-")}.json`;
    if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
      const href = URL.createObjectURL(exportBlob);
      const anchor = document.createElement("a");
      anchor.href = href;
      anchor.download = exportName;
      anchor.click();
      URL.revokeObjectURL(href);
    }
    setExportRefs((current) => [
      ...current,
      {
        name: exportName,
        created_at: exportedAt,
        item_count: items.length,
        decision_count: buildOrderedDecisionArray(items, decisions).length,
        size_bytes: exportBlob.size,
      },
    ]);
    setMessage(`Exported audit snapshot as ${exportName}.`);
  }

  function handleSubmit() {
    if (submitDisabled || !currentItem) {
      return;
    }
    if (roomID && queueID) {
      clearApprovalQueueDraft(roomID, queueID);
    }
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        queue_id: queueID,
        current_index: currentIndex,
        decisions: buildOrderedDecisionArray(items, decisions),
        notes,
        export_refs: exportRefs,
      },
      completedAt: new Date().toISOString(),
    });
  }

  return (
    <Card
      data-testid="approval-queue-root"
      className="mx-auto w-full max-w-7xl border-zinc-800 bg-zinc-950 text-zinc-100"
    >
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? envelope.data?.title ?? "Approval Queue"}</CardTitle>
        <p className="text-sm text-zinc-400">
          {envelope.data?.intent ?? "Review each queued item and submit a durable decision set."}
        </p>
        <p className="text-xs text-zinc-500">
          {items.length} item{items.length === 1 ? "" : "s"} · {unresolvedCount} unresolved
        </p>
        {message ? (
          <p data-testid="approval-queue-message" className="text-xs text-emerald-300">
            {message}
          </p>
        ) : null}
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <section className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-900/50 p-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">Queue</h3>
            <span className="text-xs text-zinc-500">
              {currentIndex + 1}/{Math.max(items.length, 1)}
            </span>
          </div>
          <div className="space-y-2">
            {items.map((item, index) => {
              const decision = decisions[item.id]?.decision;
              return (
                <button
                  key={item.id}
                  type="button"
                  data-testid={`approval-queue-item-${item.id}`}
                  onClick={() => {
                    setEvidenceIndex(0);
                    setCurrentIndex(index);
                  }}
                  className={cn(
                    "w-full rounded-md border px-3 py-2 text-left transition",
                    index === currentIndex
                      ? "border-amber-300 bg-amber-400/10"
                      : "border-zinc-800 bg-zinc-950 hover:border-zinc-700",
                  )}
                >
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <div className="text-sm font-medium">{item.title}</div>
                      <div className="text-xs text-zinc-400">{item.summary}</div>
                    </div>
                    <span className="text-[10px] uppercase tracking-[0.2em] text-zinc-500">
                      {decision ?? "pending"}
                    </span>
                  </div>
                </button>
              );
            })}
          </div>
          <div className="space-y-2 rounded-md border border-zinc-800 p-3">
            <div className="text-xs font-medium uppercase tracking-[0.2em] text-zinc-500">
              Batch
            </div>
            <select
              data-testid="approval-queue-batch-decision"
              className="w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm"
              value={batchDecision}
              onChange={(event) => setBatchDecision(event.target.value as ApprovalDecision)}
            >
              <option value="accept">Accept unresolved</option>
              <option value="reject">Reject unresolved</option>
              <option value="defer">Defer unresolved</option>
            </select>
            {batchDecision === "defer" ? (
              <Input
                data-testid="approval-queue-batch-defer-reason"
                value={batchDeferReason}
                onChange={(event) => setBatchDeferReason(event.target.value)}
                placeholder="Batch defer reason"
              />
            ) : null}
            <Button
              type="button"
              variant="secondary"
              data-testid="approval-queue-batch-apply"
              onClick={applyBatchDecision}
              disabled={batchDecision === "defer" && batchDeferReason.trim().length === 0}
            >
              Apply to unresolved
            </Button>
          </div>
        </section>

        <section className="space-y-4">
          {currentItem ? (
            <>
              <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="space-y-2">
                    <h3 className="text-lg font-semibold">{currentItem.title}</h3>
                    <p className="text-sm text-zinc-300">{currentItem.summary}</p>
                    <p className="text-sm text-zinc-400">{currentItem.description}</p>
                  </div>
                  <div className="text-right text-xs text-zinc-500">
                    <div>Keys</div>
                    <div>1 accept</div>
                    <div>2 reject</div>
                    <div>3 defer</div>
                  </div>
                </div>
                <div className="mt-4 flex flex-wrap gap-2">
                  {(["accept", "reject", "defer"] as ApprovalDecision[]).map((decision) => (
                    <Button
                      key={decision}
                      type="button"
                      data-testid={`approval-queue-decision-${decision}`}
                      variant={currentDecision?.decision === decision ? "default" : "secondary"}
                      onClick={() => applyDecisionEvent(currentItem.id, decision)}
                    >
                      {decision}
                    </Button>
                  ))}
                </div>
                <div className="mt-4 grid gap-3 md:grid-cols-2">
                  <div className="space-y-2">
                    <label className="text-xs text-zinc-400" htmlFor="approval-queue-action">
                      Action ID
                    </label>
                    <select
                      id="approval-queue-action"
                      data-testid="approval-queue-action"
                      className="w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm"
                      value={currentDecision?.action_id ?? ""}
                      onChange={(event) => updateCurrentDecision({ action_id: event.target.value })}
                    >
                      <option value="">None</option>
                      {currentItem.actionOptions.map((option) => (
                        <option key={option.id} value={option.id}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="space-y-2">
                    <label className="text-xs text-zinc-400" htmlFor="approval-queue-defer-reason">
                      Defer reason
                    </label>
                    <Input
                      id="approval-queue-defer-reason"
                      data-testid="approval-queue-defer-reason"
                      value={currentDecision?.defer_reason ?? ""}
                      onChange={(event) =>
                        updateCurrentDecision({ defer_reason: event.target.value })
                      }
                      placeholder="Required when deferred"
                    />
                  </div>
                </div>
                <div className="mt-3 space-y-2">
                  <label className="text-xs text-zinc-400" htmlFor="approval-queue-comment">
                    Comment
                  </label>
                  <Textarea
                    id="approval-queue-comment"
                    data-testid="approval-queue-comment"
                    value={currentDecision?.comment ?? ""}
                    onChange={(event) => updateCurrentDecision({ comment: event.target.value })}
                    placeholder="Optional reviewer comment"
                    rows={4}
                  />
                </div>
              </div>

              <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
                <div className="mb-3 flex items-center justify-between">
                  <h4 className="text-sm font-medium">Evidence</h4>
                  <div className="text-xs text-zinc-500">
                    {currentItem.evidence.length === 0
                      ? "No evidence"
                      : `${evidenceIndex + 1}/${currentItem.evidence.length}`}
                  </div>
                </div>
                {currentItem.evidence.length > 0 ? (
                  <>
                    <div className="mb-3 flex flex-wrap gap-2">
                      {currentItem.evidence.map((entry, index) => (
                        <button
                          key={entry.id}
                          type="button"
                          data-testid={`approval-queue-evidence-tab-${index}`}
                          onClick={() => setEvidenceIndex(index)}
                          className={cn(
                            "rounded-md border px-2 py-1 text-xs",
                            evidenceIndex === index
                              ? "border-sky-300 bg-sky-400/10 text-sky-100"
                              : "border-zinc-700 text-zinc-400",
                          )}
                        >
                          {entry.label}
                        </button>
                      ))}
                    </div>
                    <div
                      data-testid="approval-queue-evidence-content"
                      className="rounded-md border border-zinc-800 bg-zinc-950 p-3 text-sm text-zinc-200"
                    >
                      <div className="mb-1 text-xs uppercase tracking-[0.2em] text-zinc-500">
                        {currentItem.evidence[evidenceIndex]?.kind}
                      </div>
                      <div>{currentItem.evidence[evidenceIndex]?.content}</div>
                      {currentItem.evidence[evidenceIndex]?.uri ? (
                        <div className="mt-2 text-xs text-zinc-500">
                          {currentItem.evidence[evidenceIndex]?.uri}
                        </div>
                      ) : null}
                    </div>
                  </>
                ) : (
                  <p className="text-sm text-zinc-500">
                    This item does not include evidence panes.
                  </p>
                )}
              </div>

              <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
                <div className="mb-2 flex items-center justify-between">
                  <h4 className="text-sm font-medium">Queue Notes</h4>
                  <Button
                    type="button"
                    variant="secondary"
                    data-testid="approval-queue-export"
                    onClick={handleExportAudit}
                  >
                    Export audit
                  </Button>
                </div>
                <Textarea
                  data-testid="approval-queue-notes"
                  value={notes}
                  onChange={(event) => setNotes(event.target.value)}
                  placeholder="Optional queue-level notes"
                  rows={3}
                />
                {exportRefs.length > 0 ? (
                  <div className="mt-3 space-y-1 text-xs text-zinc-500">
                    {exportRefs.map((entry, index) => (
                      <div
                        key={`${entry.name}-${entry.created_at ?? "pending"}`}
                        data-testid={`approval-queue-export-ref-${index}`}
                      >
                        {entry.name}
                      </div>
                    ))}
                  </div>
                ) : null}
                {envelope.data?.audit_trail?.length ? (
                  <div className="mt-4">
                    <div className="mb-2 text-xs uppercase tracking-[0.2em] text-zinc-500">
                      Audit trail
                    </div>
                    <div className="space-y-1 text-xs text-zinc-400">
                      {envelope.data.audit_trail.slice(-5).map((entry, index) => (
                        <div
                          key={`${entry.at ?? "pending"}-${entry.event}-${entry.item_id ?? "room"}`}
                          data-testid={`approval-queue-audit-${index}`}
                        >
                          {entry.event} {entry.item_id ? `· ${entry.item_id}` : ""}{" "}
                          {entry.decision ? `· ${entry.decision}` : ""}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            </>
          ) : (
            <p className="text-sm text-zinc-500">No queue items were provided.</p>
          )}
        </section>
      </CardContent>
      <CardFooter className="justify-between">
        <div className="flex gap-2">
          <Button
            type="button"
            variant="secondary"
            data-testid="approval-queue-prev"
            onClick={() =>
              setCurrentIndex((value) => {
                setEvidenceIndex(0);
                return clampIndex(value - 1, items.length);
              })
            }
            disabled={currentIndex <= 0}
          >
            Previous
          </Button>
          <Button
            type="button"
            variant="secondary"
            data-testid="approval-queue-next"
            onClick={() =>
              setCurrentIndex((value) => {
                setEvidenceIndex(0);
                return clampIndex(value + 1, items.length);
              })
            }
            disabled={currentIndex >= items.length - 1}
          >
            Next
          </Button>
        </div>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="ghost"
            data-testid="approval-queue-cancel"
            onClick={onCancel}
          >
            Cancel
          </Button>
          <Button
            type="button"
            data-testid="approval-queue-submit"
            onClick={handleSubmit}
            disabled={submitDisabled}
          >
            Submit
          </Button>
        </div>
      </CardFooter>
    </Card>
  );
}

function normalizeItems(items: Array<Record<string, unknown>> | undefined): ApprovalItem[] {
  if (!Array.isArray(items)) {
    return [];
  }
  return items
    .map((item, index) => {
      const id = typeof item.id === "string" && item.id ? item.id : `item-${index + 1}`;
      return {
        id,
        title: readString(item.title) || id,
        summary: readString(item.summary) || readString(item.subtitle) || "No summary provided.",
        description:
          readString(item.description) || readString(item.body) || "No description provided.",
        evidence: normalizeEvidence(item.evidence),
        actionOptions: normalizeActionOptions(item.action_options),
      };
    })
    .filter((item) => item.id.length > 0);
}

function normalizeEvidence(raw: unknown): EvidenceItem[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: EvidenceItem[] = [];
  for (const [index, item] of raw.entries()) {
    if (!item || typeof item !== "object") {
      continue;
    }
    const record = item as Record<string, unknown>;
    const content = readString(record.content) || readString(record.summary) || "";
    if (!content) {
      continue;
    }
    out.push({
      id: readString(record.id) || `evidence-${index + 1}`,
      label: readString(record.label) || `Evidence ${index + 1}`,
      kind: readString(record.kind) || "note",
      content,
      uri: readString(record.uri) || undefined,
    });
  }
  return out;
}

function normalizeActionOptions(raw: unknown): ActionOption[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw
    .map((item) => {
      if (!item || typeof item !== "object") {
        return null;
      }
      const record = item as Record<string, unknown>;
      const id = readString(record.id);
      if (!id) {
        return null;
      }
      return { id, label: readString(record.label) || id };
    })
    .filter((item): item is ActionOption => Boolean(item));
}

function buildDecisionMap(
  decisions: PersistedDecision[] | ApprovalQueueDraftDecision[] | undefined,
) {
  const out: Record<string, DecisionState> = {};
  for (const item of decisions ?? []) {
    if (!item?.item_id) {
      continue;
    }
    out[item.item_id] = {
      decision: item.decision,
      comment: item.comment ?? "",
      action_id: item.action_id ?? "",
      defer_reason: item.defer_reason ?? "",
      decided_at: item.decided_at,
    };
  }
  return out;
}

function buildOrderedDecisionArray(
  items: ApprovalItem[],
  decisions: Record<string, DecisionState>,
): PersistedDecision[] {
  const out: PersistedDecision[] = [];
  for (const item of items) {
    const decision = decisions[item.id];
    if (!decision?.decision) {
      continue;
    }
    out.push({
      item_id: item.id,
      decision: decision.decision,
      comment: decision.comment.trim(),
      action_id: decision.action_id.trim(),
      defer_reason: decision.defer_reason.trim(),
      decided_at: decision.decided_at ?? new Date().toISOString(),
    });
  }
  return out;
}

function normalizeExportRefs(raw: unknown): ExportRef[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: ExportRef[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") {
      continue;
    }
    const record = item as Record<string, unknown>;
    const name = readString(record.name);
    if (!name) {
      continue;
    }
    out.push({
      name,
      created_at: readString(record.created_at) || undefined,
      item_count: readNumber(record.item_count),
      decision_count: readNumber(record.decision_count),
      size_bytes: readNumber(record.size_bytes),
    });
  }
  return out;
}

function clampIndex(index: number, length: number): number {
  if (!Number.isFinite(index)) {
    return 0;
  }
  const safeIndex = Math.trunc(index);
  if (length <= 0) {
    return 0;
  }
  if (safeIndex < 0) {
    return 0;
  }
  if (safeIndex >= length) {
    return length - 1;
  }
  return safeIndex;
}

function readString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function readNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function sanitizeFilePart(value: string): string {
  return value.replace(/[^a-zA-Z0-9._-]+/g, "-");
}
