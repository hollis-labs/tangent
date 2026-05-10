import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  buildDiffReviewCanonicalSeedKey,
  clearDiffReviewDraft,
  DIFF_REVIEW_AUTOSAVE_DEBOUNCE_MS,
  type DiffReviewDraftDecision,
  loadDiffReviewDraft,
  saveDiffReviewDraft,
} from "@/lib/diff-review-draft-storage";
import { cn } from "@/lib/utils";

type DiffDecision = "accept" | "reject" | "comment";
type FilterDecision = "all" | "pending" | DiffDecision;

type ArtifactRef = {
  artifact_id?: string;
  name?: string;
  uri?: string;
  mime_type?: string;
  kind?: string;
  size_bytes?: number;
};

type ActionOption = {
  id: string;
  label: string;
};

type HunkItem = {
  id: string;
  header: string;
  before: string;
  after: string;
  status?: string;
  action_options?: ActionOption[];
};

type FileItem = {
  id: string;
  path: string;
  summary: string;
  before: string;
  after: string;
  action_options?: ActionOption[];
  hunks: HunkItem[];
};

type PersistedDecision = {
  file_id: string;
  hunk_id?: string;
  decision: DiffDecision;
  comment?: string;
  action_id?: string;
  decided_at?: string;
};

type FilterState = {
  search?: string;
  decision?: FilterDecision;
};

type Summary = {
  submitted_at?: string;
  decision_file_count?: number;
  decision_hunk_count?: number;
  comment_count?: number;
  pending_count?: number;
  decision_summary?: Record<string, number>;
  export_text?: string;
  export_name?: string;
};

type DecisionState = {
  decision?: DiffDecision;
  action_id: string;
  decided_at?: string;
};

export interface DiffReviewEnvelope {
  v: number;
  id: string;
  type: "tangent.diff-review";
  title?: string;
  context?: string;
  data?: {
    review_id: string;
    title?: string;
    intent?: string;
    files?: Array<Record<string, unknown>>;
    current_file?: string;
    filter_state?: FilterState;
    decisions?: PersistedDecision[];
    comments?: Record<string, string>;
    updated_at?: string;
    summary?: Summary;
    before_ref?: ArtifactRef;
    after_ref?: ArtifactRef;
    export_refs?: ArtifactRef[];
  };
}

export interface DiffReviewResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    review_id: string;
    current_file?: string;
    filter_state: FilterState;
    decisions: PersistedDecision[];
    comments: Record<string, string>;
    summary: Summary;
    before_ref?: ArtifactRef;
    after_ref?: ArtifactRef;
    export_refs: ArtifactRef[];
  };
  completedAt?: string;
}

export type DiffReviewProps = {
  envelope: DiffReviewEnvelope;
  onSubmit: (response: DiffReviewResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function DiffReview({ envelope, onSubmit, onCancel, roomID }: DiffReviewProps) {
  const reviewID = envelope.data?.review_id ?? "";
  const files = useMemo(() => normalizeFiles(envelope.data?.files), [envelope.data?.files]);
  const canonicalFilterState = normalizeFilterState(envelope.data?.filter_state);
  const canonicalComments = normalizeComments(envelope.data?.comments);
  const persistedDecisions = useMemo(
    () => buildDecisionMap(envelope.data?.decisions),
    [envelope.data?.decisions],
  );
  const baseSeedKey = useMemo(
    () =>
      buildDiffReviewCanonicalSeedKey({
        files,
        currentFile: envelope.data?.current_file ?? "",
        filterState: canonicalFilterState,
        comments: canonicalComments,
      }),
    [files, envelope.data?.current_file, canonicalFilterState, canonicalComments],
  );
  const draft = roomID && reviewID ? loadDiffReviewDraft(roomID, reviewID) : null;
  const recoveredDraft =
    draft && (draft.envelopeId === envelope.id || draft.baseSeedKey === baseSeedKey) ? draft : null;

  const [currentFile, setCurrentFile] = useState(() =>
    coerceCurrentFile(recoveredDraft?.currentFile ?? envelope.data?.current_file, files),
  );
  const [filterState, setFilterState] = useState<FilterState>(() =>
    normalizeFilterState(
      recoveredDraft?.filterState as FilterState | undefined,
      canonicalFilterState,
    ),
  );
  const [decisions, setDecisions] = useState<Record<string, DecisionState>>(() =>
    recoveredDraft
      ? buildDecisionMap(recoveredDraft.decisions as PersistedDecision[])
      : persistedDecisions,
  );
  const [comments, setComments] = useState<Record<string, string>>(() =>
    recoveredDraft ? normalizeComments(recoveredDraft.comments) : canonicalComments,
  );
  const [exportRefs, setExportRefs] = useState<ArtifactRef[]>(
    recoveredDraft
      ? normalizeArtifactRefs(recoveredDraft.exportRefs as ArtifactRef[])
      : normalizeArtifactRefs(envelope.data?.export_refs),
  );
  const [message, setMessage] = useState<string | null>(
    recoveredDraft ? "Recovered unsent diff-review state from this browser." : null,
  );
  const draftJsonRef = useRef<string | null>(null);

  const filteredFiles = useMemo(
    () => applyFileFilters(files, filterState, decisions),
    [files, filterState, decisions],
  );
  const activeFile = useMemo(() => {
    const direct = files.find((file) => file.id === currentFile) ?? null;
    if (direct) {
      return direct;
    }
    return filteredFiles[0] ?? null;
  }, [files, currentFile, filteredFiles]);

  useEffect(() => {
    if (!activeFile) {
      return;
    }
    if (currentFile !== activeFile.id) {
      setCurrentFile(activeFile.id);
    }
  }, [activeFile, currentFile]);

  useEffect(() => {
    if (!roomID || !reviewID) {
      return;
    }
    const timerID = window.setTimeout(() => {
      const next = JSON.stringify({
        currentFile,
        filterState,
        decisions: buildOrderedDecisions(files, decisions),
        comments,
        exportRefs,
      });
      if (next === draftJsonRef.current) {
        return;
      }
      const canonical = JSON.stringify({
        currentFile: coerceCurrentFile(envelope.data?.current_file, files),
        filterState: canonicalFilterState,
        decisions: buildOrderedDecisions(files, persistedDecisions),
        comments: canonicalComments,
        exportRefs: normalizeArtifactRefs(envelope.data?.export_refs),
      });
      if (next === canonical) {
        clearDiffReviewDraft(roomID, reviewID);
        draftJsonRef.current = null;
        return;
      }
      saveDiffReviewDraft({
        version: 1,
        roomID,
        reviewID,
        envelopeId: envelope.id,
        baseSeedKey,
        currentFile,
        filterState,
        decisions: buildOrderedDecisions(files, decisions) as DiffReviewDraftDecision[],
        comments,
        exportRefs: exportRefs as Array<Record<string, unknown>>,
        savedAt: new Date().toISOString(),
      });
      draftJsonRef.current = next;
    }, DIFF_REVIEW_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(timerID);
  }, [
    roomID,
    reviewID,
    envelope.id,
    baseSeedKey,
    currentFile,
    filterState,
    decisions,
    comments,
    exportRefs,
    files,
    envelope.data?.current_file,
    envelope.data?.export_refs,
    persistedDecisions,
    canonicalFilterState,
    canonicalComments,
  ]);

  const submitDisabled = files.length === 0 || buildOrderedDecisions(files, decisions).length === 0;

  const setDecision = (key: string, decision: DiffDecision) => {
    setDecisions((current) => ({
      ...current,
      [key]: {
        ...(current[key] ?? { action_id: "" }),
        decision,
        decided_at: new Date().toISOString(),
      },
    }));
  };

  const applyBatchDecision = (decision: DiffDecision) => {
    if (!activeFile) {
      return;
    }
    setDecisions((current) => {
      const next = { ...current };
      const targets = activeFile.hunks.length
        ? activeFile.hunks.map((hunk) => buildTargetKey(activeFile.id, hunk.id))
        : [activeFile.id];
      for (const target of targets) {
        next[target] = {
          ...(current[target] ?? { action_id: "" }),
          decision,
          decided_at: new Date().toISOString(),
        };
      }
      return next;
    });
  };

  const handleExport = () => {
    const text = buildSummaryText(files, decisions, comments);
    const stamp = new Date().toISOString().replaceAll(":", "-");
    const name = `${reviewID || "diff-review"}-summary-${stamp}.md`;
    const blob = new Blob([text], { type: "text/markdown" });
    const href = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = href;
    link.download = name;
    link.click();
    URL.revokeObjectURL(href);
    setExportRefs((current) => [...current, { name, mime_type: "text/markdown", kind: "summary" }]);
    setMessage(`Exported ${name}`);
  };

  const handleSubmit = () => {
    const summary = buildSummary(files, decisions, comments);
    summary.export_text = buildSummaryText(files, decisions, comments);
    summary.export_name = exportRefs[exportRefs.length - 1]?.name;
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      completedAt: new Date().toISOString(),
      payload: {
        review_id: reviewID,
        current_file: activeFile?.id ?? currentFile,
        filter_state: filterState,
        decisions: buildOrderedDecisions(files, decisions),
        comments,
        summary,
        before_ref: envelope.data?.before_ref,
        after_ref: envelope.data?.after_ref,
        export_refs: exportRefs,
      },
    });
    if (roomID && reviewID) {
      clearDiffReviewDraft(roomID, reviewID);
    }
  };

  return (
    <div className="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]" data-testid="diff-review-root">
      <Card className="border-zinc-800 bg-zinc-900/70">
        <CardHeader className="space-y-3">
          <CardTitle className="text-base text-zinc-50">
            {envelope.title ?? envelope.data?.title ?? "Diff review"}
          </CardTitle>
          <div className="space-y-2">
            <Input
              data-testid="diff-review-search"
              placeholder="Filter files"
              value={filterState.search ?? ""}
              onChange={(event) =>
                setFilterState((current) => ({ ...current, search: event.target.value }))
              }
            />
            <select
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100"
              data-testid="diff-review-filter"
              value={filterState.decision ?? "all"}
              onChange={(event) =>
                setFilterState((current) => ({
                  ...current,
                  decision: event.target.value as FilterDecision,
                }))
              }
            >
              <option value="all">All files</option>
              <option value="pending">Pending</option>
              <option value="accept">Accepted</option>
              <option value="reject">Rejected</option>
              <option value="comment">Commented</option>
            </select>
          </div>
          {message ? (
            <p className="text-xs text-emerald-300" data-testid="diff-review-message">
              {message}
            </p>
          ) : null}
          {envelope.data?.before_ref || envelope.data?.after_ref ? (
            <div className="rounded-md border border-zinc-800 bg-zinc-950/80 p-3 text-xs text-zinc-300">
              {renderArtifactLabel("Before", envelope.data?.before_ref)}
              {renderArtifactLabel("After", envelope.data?.after_ref)}
            </div>
          ) : null}
        </CardHeader>
        <CardContent className="space-y-2">
          {filteredFiles.length === 0 ? (
            <p className="text-sm text-zinc-500">No files match the current filter.</p>
          ) : (
            filteredFiles.map((file) => {
              const stats = summarizeFile(file, decisions);
              return (
                <button
                  key={file.id}
                  type="button"
                  data-testid={`diff-review-file-${file.id}`}
                  onClick={() => setCurrentFile(file.id)}
                  className={cn(
                    "w-full rounded-lg border p-3 text-left transition",
                    file.id === activeFile?.id
                      ? "border-amber-400 bg-amber-500/10"
                      : "border-zinc-800 bg-zinc-950 hover:border-zinc-700",
                  )}
                >
                  <div className="text-sm font-medium text-zinc-100">{file.path}</div>
                  {file.summary ? (
                    <div className="mt-1 text-xs text-zinc-400">{file.summary}</div>
                  ) : null}
                  <div className="mt-2 text-[11px] text-zinc-500">
                    {stats.decided}/{stats.total} decided
                  </div>
                </button>
              );
            })
          )}
        </CardContent>
      </Card>

      <Card className="border-zinc-800 bg-zinc-900/70">
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle className="text-base text-zinc-50">
                {activeFile?.path ?? "No file selected"}
              </CardTitle>
              {activeFile?.summary ? (
                <p className="text-sm text-zinc-400">{activeFile.summary}</p>
              ) : null}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                data-testid="diff-review-batch-accept"
                onClick={() => applyBatchDecision("accept")}
                disabled={!activeFile}
              >
                Accept File
              </Button>
              <Button
                type="button"
                variant="outline"
                data-testid="diff-review-batch-reject"
                onClick={() => applyBatchDecision("reject")}
                disabled={!activeFile}
              >
                Request Changes
              </Button>
              <Button
                type="button"
                variant="outline"
                data-testid="diff-review-export"
                onClick={handleExport}
                disabled={files.length === 0}
              >
                Export Summary
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {!activeFile ? (
            <p className="text-sm text-zinc-500">No diff files were provided.</p>
          ) : activeFile.hunks.length === 0 ? (
            <DiffReviewTargetCard
              prefix={`diff-review-file-decision-${activeFile.id}`}
              title={activeFile.path}
              before={activeFile.before}
              after={activeFile.after}
              decision={decisions[activeFile.id]}
              comment={comments[activeFile.id] ?? ""}
              actionOptions={activeFile.action_options}
              onDecision={(decision) => setDecision(activeFile.id, decision)}
              onActionID={(value) =>
                setDecisions((current) => ({
                  ...current,
                  [activeFile.id]: {
                    ...(current[activeFile.id] ?? { action_id: "" }),
                    action_id: value,
                  },
                }))
              }
              onComment={(value) =>
                setComments((current) => ({ ...current, [activeFile.id]: value }))
              }
            />
          ) : (
            activeFile.hunks.map((hunk) => {
              const key = buildTargetKey(activeFile.id, hunk.id);
              return (
                <DiffReviewTargetCard
                  key={key}
                  prefix={`diff-review-hunk-decision-${hunk.id}`}
                  title={`${activeFile.path} ${hunk.header}`.trim()}
                  before={hunk.before || activeFile.before}
                  after={hunk.after || activeFile.after}
                  decision={decisions[key]}
                  comment={comments[key] ?? ""}
                  actionOptions={hunk.action_options ?? activeFile.action_options}
                  onDecision={(decision) => setDecision(key, decision)}
                  onActionID={(value) =>
                    setDecisions((current) => ({
                      ...current,
                      [key]: { ...(current[key] ?? { action_id: "" }), action_id: value },
                    }))
                  }
                  onComment={(value) => setComments((current) => ({ ...current, [key]: value }))}
                />
              );
            })
          )}
          <div className="flex justify-end gap-3">
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              data-testid="diff-review-submit"
              onClick={handleSubmit}
              disabled={submitDisabled}
            >
              Submit Review
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function DiffReviewTargetCard({
  prefix,
  title,
  before,
  after,
  decision,
  comment,
  actionOptions,
  onDecision,
  onActionID,
  onComment,
}: {
  prefix: string;
  title: string;
  before: string;
  after: string;
  decision?: DecisionState;
  comment: string;
  actionOptions?: ActionOption[];
  onDecision: (decision: DiffDecision) => void;
  onActionID: (value: string) => void;
  onComment: (value: string) => void;
}) {
  return (
    <Card className="border-zinc-800 bg-zinc-950/80">
      <CardHeader>
        <CardTitle className="text-sm text-zinc-100">{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 lg:grid-cols-2">
          <DiffPane title="Before" content={before} />
          <DiffPane title="After" content={after} />
        </div>
        <div className="space-y-3">
          <div className="flex flex-wrap gap-2">
            {(["accept", "reject", "comment"] as const).map((value) => (
              <Button
                key={value}
                type="button"
                variant={decision?.decision === value ? "default" : "outline"}
                data-testid={`${prefix}-${value}`}
                onClick={() => onDecision(value)}
              >
                {value === "accept" ? "Accept" : value === "reject" ? "Request Changes" : "Comment"}
              </Button>
            ))}
          </div>
          {actionOptions?.length ? (
            <select
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100"
              value={decision?.action_id ?? ""}
              onChange={(event) => onActionID(event.target.value)}
            >
              <option value="">No action ID</option>
              {actionOptions.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.label}
                </option>
              ))}
            </select>
          ) : null}
          <Textarea
            data-testid={`${prefix}-notes`}
            value={comment}
            placeholder="Leave a review note"
            onChange={(event) => onComment(event.target.value)}
          />
        </div>
      </CardContent>
    </Card>
  );
}

function DiffPane({ title, content }: { title: string; content: string }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/60">
      <div className="border-b border-zinc-800 px-3 py-2 text-xs uppercase tracking-[0.16em] text-zinc-400">
        {title}
      </div>
      <pre className="overflow-x-auto whitespace-pre-wrap p-3 text-xs text-zinc-200">
        {content || " "}
      </pre>
    </div>
  );
}

function normalizeFiles(raw: Array<Record<string, unknown>> | undefined): FileItem[] {
  if (!raw?.length) {
    return [];
  }
  const out: FileItem[] = [];
  for (const record of raw) {
    const id = readString(record.id);
    if (!id) {
      continue;
    }
    out.push({
      id,
      path: readString(record.path) || id,
      summary: readString(record.summary),
      before: readString(record.before),
      after: readString(record.after),
      action_options: normalizeActionOptions(record.action_options),
      hunks: normalizeHunks(record.hunks),
    });
  }
  return out;
}

function normalizeHunks(raw: unknown): HunkItem[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: HunkItem[] = [];
  for (const value of raw) {
    if (!value || typeof value !== "object") {
      continue;
    }
    const record = value as Record<string, unknown>;
    const id = readString(record.id);
    if (!id) {
      continue;
    }
    out.push({
      id,
      header: readString(record.header),
      before: readString(record.before),
      after: readString(record.after),
      status: readString(record.status),
      action_options: normalizeActionOptions(record.action_options),
    });
  }
  return out;
}

function normalizeActionOptions(raw: unknown): ActionOption[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw
    .map((value) => {
      if (!value || typeof value !== "object") {
        return null;
      }
      const record = value as Record<string, unknown>;
      const id = readString(record.id);
      if (!id) {
        return null;
      }
      return { id, label: readString(record.label) || id };
    })
    .filter((item): item is ActionOption => item !== null);
}

function normalizeFilterState(
  raw: FilterState | undefined,
  fallback: FilterState = {},
): FilterState {
  return {
    search: typeof raw?.search === "string" ? raw.search : (fallback.search ?? ""),
    decision:
      raw?.decision === "pending" ||
      raw?.decision === "accept" ||
      raw?.decision === "reject" ||
      raw?.decision === "comment"
        ? raw.decision
        : (fallback.decision ?? "all"),
  };
}

function normalizeComments(raw: Record<string, string> | undefined): Record<string, string> {
  if (!raw) {
    return {};
  }
  return Object.fromEntries(
    Object.entries(raw)
      .map(([key, value]) => [key.trim(), typeof value === "string" ? value : ""])
      .filter(([key, value]) => key && value.trim().length > 0),
  );
}

function normalizeArtifactRefs(
  raw: ArtifactRef[] | Array<Record<string, unknown>> | undefined,
): ArtifactRef[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: ArtifactRef[] = [];
  for (const value of raw) {
    if (!value || typeof value !== "object") {
      continue;
    }
    const record = value as Record<string, unknown>;
    const item: ArtifactRef = {
      artifact_id: readString(record.artifact_id),
      name: readString(record.name),
      uri: readString(record.uri),
      mime_type: readString(record.mime_type),
      kind: readString(record.kind),
    };
    if (!item.name && !item.uri && !item.artifact_id) {
      continue;
    }
    out.push(item);
  }
  return out;
}

function buildDecisionMap(items: PersistedDecision[] | undefined): Record<string, DecisionState> {
  if (!items?.length) {
    return {};
  }
  return Object.fromEntries(
    items.map((item) => [
      buildTargetKey(item.file_id, item.hunk_id),
      {
        decision: item.decision,
        action_id: item.action_id ?? "",
        decided_at: item.decided_at,
      },
    ]),
  );
}

function applyFileFilters(
  files: FileItem[],
  filterState: FilterState,
  decisions: Record<string, DecisionState>,
): FileItem[] {
  const search = (filterState.search ?? "").trim().toLowerCase();
  return files.filter((file) => {
    if (search && !`${file.path} ${file.summary}`.toLowerCase().includes(search)) {
      return false;
    }
    const decisionFilter = filterState.decision ?? "all";
    if (decisionFilter === "all") {
      return true;
    }
    const fileDecisions = file.hunks.length
      ? file.hunks.map((hunk) => decisions[buildTargetKey(file.id, hunk.id)]?.decision)
      : [decisions[file.id]?.decision];
    if (decisionFilter === "pending") {
      return fileDecisions.some((decision) => !decision);
    }
    return fileDecisions.some((decision) => decision === decisionFilter);
  });
}

function buildOrderedDecisions(
  files: FileItem[],
  decisions: Record<string, DecisionState>,
): PersistedDecision[] {
  const out: PersistedDecision[] = [];
  for (const file of files) {
    if (file.hunks.length === 0) {
      const decision = decisions[file.id];
      if (!decision?.decision) {
        continue;
      }
      out.push({
        file_id: file.id,
        decision: decision.decision,
        action_id: decision.action_id || undefined,
        decided_at: decision.decided_at,
      });
      continue;
    }
    for (const hunk of file.hunks) {
      const decision = decisions[buildTargetKey(file.id, hunk.id)];
      if (!decision?.decision) {
        continue;
      }
      out.push({
        file_id: file.id,
        hunk_id: hunk.id,
        decision: decision.decision,
        action_id: decision.action_id || undefined,
        decided_at: decision.decided_at,
      });
    }
  }
  return out;
}

function buildSummary(
  files: FileItem[],
  decisions: Record<string, DecisionState>,
  comments: Record<string, string>,
): Summary {
  const ordered = buildOrderedDecisions(files, decisions);
  const decisionSummary: Record<string, number> = {};
  const fileIDs = new Set<string>();
  let totalTargets = 0;
  for (const file of files) {
    totalTargets += file.hunks.length || 1;
  }
  for (const decision of ordered) {
    decisionSummary[decision.decision] = (decisionSummary[decision.decision] ?? 0) + 1;
    fileIDs.add(decision.file_id);
  }
  return {
    submitted_at: new Date().toISOString(),
    decision_file_count: fileIDs.size,
    decision_hunk_count: ordered.length,
    comment_count: Object.keys(comments).length,
    pending_count: Math.max(totalTargets - ordered.length, 0),
    decision_summary: decisionSummary,
  };
}

function buildSummaryText(
  files: FileItem[],
  decisions: Record<string, DecisionState>,
  comments: Record<string, string>,
): string {
  const ordered = buildOrderedDecisions(files, decisions);
  const lines = ["# Diff review summary", ""];
  for (const file of files) {
    lines.push(`## ${file.path}`);
    const targets = file.hunks.length
      ? file.hunks.map((hunk) => ({
          label: hunk.header || hunk.id,
          key: buildTargetKey(file.id, hunk.id),
        }))
      : [{ label: "file", key: file.id }];
    for (const target of targets) {
      const decision = ordered.find(
        (item) => buildTargetKey(item.file_id, item.hunk_id) === target.key,
      );
      const comment = comments[target.key];
      lines.push(`- ${target.label}: ${decision?.decision ?? "pending"}`);
      if (decision?.action_id) {
        lines.push(`  action_id: ${decision.action_id}`);
      }
      if (comment) {
        lines.push(`  comment: ${comment}`);
      }
    }
    lines.push("");
  }
  return lines.join("\n");
}

function summarizeFile(file: FileItem, decisions: Record<string, DecisionState>) {
  const targets = file.hunks.length
    ? file.hunks.map((hunk) => buildTargetKey(file.id, hunk.id))
    : [file.id];
  const decided = targets.filter((target) => decisions[target]?.decision).length;
  return { total: targets.length, decided };
}

function coerceCurrentFile(currentFile: string | undefined, files: FileItem[]): string {
  if (currentFile && files.some((file) => file.id === currentFile)) {
    return currentFile;
  }
  return files[0]?.id ?? "";
}

function renderArtifactLabel(label: string, ref: ArtifactRef | undefined) {
  if (!ref?.name && !ref?.uri && !ref?.artifact_id) {
    return null;
  }
  return (
    <div>
      <span className="font-medium text-zinc-200">{label}:</span>{" "}
      <span>{ref.name ?? ref.uri ?? ref.artifact_id}</span>
    </div>
  );
}

function buildTargetKey(fileID: string, hunkID?: string) {
  return hunkID ? `${fileID}::${hunkID}` : fileID;
}

function readString(value: unknown): string {
  return typeof value === "string" ? value : "";
}
