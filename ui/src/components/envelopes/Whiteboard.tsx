import { type ChangeEvent, useEffect, useEffectEvent, useRef, useState } from "react";
import { type Editor, getSnapshot, type TLEditorSnapshot, Tldraw } from "tldraw";
import "tldraw/tldraw.css";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import { buildSubmitGate, type SubmitRequirement, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";
import {
  buildWhiteboardSubmitAssets,
  mergeWhiteboardAssetRefs,
  prepareWhiteboardSnapshotForEditor,
  type WhiteboardAssetRef,
  type WhiteboardExportRef,
} from "@/lib/whiteboard-assets";
import {
  buildWhiteboardCanonicalSeedKey,
  clearWhiteboardDraft,
  loadWhiteboardDraft,
  saveWhiteboardDraft,
  WHITEBOARD_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/whiteboard-draft-storage";

export interface WhiteboardRevisionHistoryItem {
  revision_id: string;
  continued_from_revision_id?: string;
  updated_at?: string;
  summary?: string;
  scene_size?: number;
  asset_count?: number;
}

export interface WhiteboardRevisionSnapshotItem extends WhiteboardRevisionHistoryItem {
  scene?: TLEditorSnapshot | Record<string, unknown>;
  assets?: WhiteboardAssetRef[];
  reference_images?: WhiteboardAssetRef[];
  export_refs?: WhiteboardExportRef[];
  notes?: string;
}

export interface WhiteboardSelectionSummary {
  count: number;
  ids?: string[];
  types?: string[];
}

export interface WhiteboardEnvelopeData {
  board_id: string;
  title?: string;
  intent?: string;
  scene?: TLEditorSnapshot | Record<string, unknown>;
  assets?: WhiteboardAssetRef[];
  export_refs?: WhiteboardExportRef[];
  notes?: string;
  updated_at?: string;
  revision_id?: string;
  revision_history?: WhiteboardRevisionHistoryItem[];
  revisions?: WhiteboardRevisionSnapshotItem[];
  tool_mode?: "select" | "draw" | "text" | "shape" | "arrow" | "note";
  reference_images?: WhiteboardAssetRef[];
}

export interface WhiteboardEnvelope {
  v: number;
  id: string;
  type: "tangent.whiteboard";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: WhiteboardEnvelopeData;
  meta?: Record<string, unknown>;
}

export interface WhiteboardSubmitResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    board_id: string;
    scene: TLEditorSnapshot;
    assets: WhiteboardAssetRef[];
    notes: string;
    tool_mode?: WhiteboardEnvelopeData["tool_mode"];
    selection_summary?: WhiteboardSelectionSummary;
    continued_from_revision_id?: string;
    export_refs?: WhiteboardExportRef[];
  };
  completedAt: string;
}

export type WhiteboardProps = {
  envelope: WhiteboardEnvelope;
  onSubmit: (response: WhiteboardSubmitResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

const NOTES_ID = "whiteboard-notes";
const CANVAS_ID = "whiteboard-canvas";

export function Whiteboard({ envelope, onSubmit, onCancel, roomID }: WhiteboardProps) {
  const data = envelope.data;
  const editorRef = useRef<Editor | null>(null);
  const boardID = data?.board_id ?? "";
  const assetRefs = mergeWhiteboardAssetRefs(data?.assets ?? [], data?.reference_images ?? []);
  const revisions = data?.revisions ?? [];
  const latestRevisionID = readRevisionID(data?.revision_id);
  const [initialState] = useState(() =>
    resolveInitialWhiteboardState({
      roomID,
      boardID,
      envelopeId: envelope.id,
      scene: normalizeSnapshot(data?.scene),
      assetRefs,
      notes: data?.notes ?? "",
      revisionId: readRevisionID(data?.revision_id),
    }),
  );
  const [editorSeed, setEditorSeed] = useState<TLEditorSnapshot | undefined>(initialState.scene);
  const [editorSeedKey, setEditorSeedKey] = useState(() =>
    buildEditorSeedKey(initialState.revisionId, "latest"),
  );
  const [activeAssetRefs, setActiveAssetRefs] = useState<WhiteboardAssetRef[]>(assetRefs);
  const [notes, setNotes] = useState(initialState.notes);
  const [latestExportRef, setLatestExportRef] = useState<WhiteboardExportRef | null>(null);
  const [exportStatus, setExportStatus] = useState<"idle" | "done" | "failed">("idle");
  const [selectedRevisionId, setSelectedRevisionId] = useState<string | null>(
    initialState.revisionId,
  );
  const [activeRevisionId, setActiveRevisionId] = useState<string | null>(initialState.revisionId);
  const [continuedFromRevisionId, setContinuedFromRevisionId] = useState<string | null>(null);
  const [revisionMode, setRevisionMode] = useState<"latest" | "preview" | "continue">("latest");
  const [message, setMessage] = useState<string | null>(
    initialState.staleReferenceImages.length > 0
      ? buildStaleReferenceImageMessage(initialState.staleReferenceImages.length)
      : null,
  );
  // Two of this workflow's submit guards cannot be derived from render state:
  // whether the tldraw editor has mounted, and whether the current scene still
  // carries browser-only image assets — the latter is only known once the
  // snapshot is taken. They are recorded here so the gate notice beside the CTA
  // can explain a click that did nothing, instead of leaving the operator with
  // a banner 640 pixels above the button or with no feedback at all.
  const [attemptBlock, setAttemptBlockState] = useState<SubmitRequirement | null>(null);
  // Mirrored in a ref so the store listener can skip the state update entirely
  // when there is nothing recorded — a no-op setState still schedules a render.
  const attemptBlockRef = useRef<SubmitRequirement | null>(null);
  const setAttemptBlock = (next: SubmitRequirement | null) => {
    if (attemptBlockRef.current === null && next === null) {
      return;
    }
    attemptBlockRef.current = next;
    setAttemptBlockState(next);
  };
  const notesRef = useRef(initialState.notes);
  const autosaveTimerRef = useRef<number | null>(null);
  const unsubscribeRef = useRef<(() => void) | null>(null);
  const shouldFlushDraftRef = useRef(Boolean(roomID && boardID));
  const canonicalSceneRef = useRef<TLEditorSnapshot | undefined>(initialState.canonicalScene);
  const canonicalNotesRef = useRef(initialState.canonicalNotes);
  const canonicalRevisionIdRef = useRef(initialState.revisionId);
  const canonicalSeedKeyRef = useRef(initialState.canonicalSeedKey);
  const latestExportRefRef = useRef<WhiteboardExportRef | null>(null);
  const lastPersistedPayloadRef = useRef<string | null>(
    initialState.recoveredDraft
      ? JSON.stringify({
          notes: initialState.notes,
          scene: initialState.scene,
        })
      : null,
  );

  const clearDraft = useEffectEvent(() => {
    if (!roomID || !boardID) {
      return;
    }
    clearWhiteboardDraft(roomID, boardID);
    lastPersistedPayloadRef.current = null;
  });

  const persistDraft = useEffectEvent(() => {
    if (!roomID || !boardID) {
      return;
    }
    const editor = editorRef.current;
    if (!editor) {
      return;
    }
    const scene = getSnapshot(editor.store);
    const nextNotes = notesRef.current;
    const payloadKey = JSON.stringify({ notes: nextNotes, scene });
    if (payloadKey === lastPersistedPayloadRef.current) {
      return;
    }

    const canonicalPayloadKey = JSON.stringify({
      notes: canonicalNotesRef.current,
      scene: canonicalSceneRef.current ?? null,
    });
    if (payloadKey === canonicalPayloadKey) {
      clearDraft();
      return;
    }

    saveWhiteboardDraft({
      version: 1,
      roomID,
      boardID,
      envelopeId: envelope.id,
      baseRevisionId: canonicalRevisionIdRef.current,
      baseSeedKey: canonicalSeedKeyRef.current,
      notes: nextNotes,
      scene,
      savedAt: new Date().toISOString(),
    });
    lastPersistedPayloadRef.current = payloadKey;
  });

  const scheduleAutosave = useEffectEvent(() => {
    if (!roomID || !boardID) {
      return;
    }
    if (autosaveTimerRef.current !== null) {
      window.clearTimeout(autosaveTimerRef.current);
    }
    autosaveTimerRef.current = window.setTimeout(() => {
      autosaveTimerRef.current = null;
      persistDraft();
    }, WHITEBOARD_AUTOSAVE_DEBOUNCE_MS);
  });

  useEffect(() => {
    return () => {
      unsubscribeRef.current?.();
      if (autosaveTimerRef.current !== null) {
        window.clearTimeout(autosaveTimerRef.current);
        autosaveTimerRef.current = null;
      }
      if (shouldFlushDraftRef.current) {
        persistDraft();
      }
    };
  }, []);

  const clearAttemptBlock = useEffectEvent(() => {
    setAttemptBlock(null);
  });

  const invalidateExport = useEffectEvent(() => {
    if (latestExportRefRef.current === null) {
      return;
    }
    latestExportRefRef.current = null;
    setLatestExportRef(null);
    setExportStatus("idle");
  });

  const revealRequirement = useRevealRequirement();

  // Preview mode is what actually disables Submit board, and the control that
  // clears it — "Continue from here" — sits at the bottom of a card whose
  // canvas alone is 640px tall, while the explanation used to render at the very
  // top. The gate puts the reason next to the button and the button next to the
  // control.
  const gate = buildSubmitGate([
    !boardID && {
      controlID: "",
      label: "board",
      message: "this envelope carries no board id, so there is nothing to submit.",
    },
    revisionMode === "preview" && {
      controlID: activeRevisionId ? `whiteboard-continue-${activeRevisionId}` : "",
      label: "Continue from here",
      message: `revision ${activeRevisionId ?? "preview"} is open for inspection only — continue from it to make edits submittable.`,
    },
    attemptBlock,
  ]);

  const handleSubmit = () => {
    if (gate.blocked) {
      revealRequirement(gate.first);
      return;
    }
    const editor = editorRef.current;
    if (!editor || !data?.board_id) {
      // Previously a silent return: the button was live, the click did nothing,
      // and nothing on screen changed.
      setAttemptBlock({
        controlID: CANVAS_ID,
        label: "board",
        message: "the board editor has not finished loading yet.",
      });
      return;
    }
    const submitScene = buildWhiteboardSubmitAssets(getSnapshot(editor.store), activeAssetRefs);
    if (submitScene.unsupportedLocalAssetIds.length > 0) {
      const count = submitScene.unsupportedLocalAssetIds.length;
      setMessage(
        `Remove ${count} browser-only image asset(s) before submit. Tangent only persists artifact-backed image refs.`,
      );
      setAttemptBlock({
        controlID: CANVAS_ID,
        label: "board",
        message: `${count} browser-only image asset${count === 1 ? "" : "s"} must be removed from the board first.`,
      });
      return;
    }
    setAttemptBlock(null);
    const selectionSummary = summarizeSelection(editor);
    shouldFlushDraftRef.current = false;
    clearDraft();
    setMessage(null);
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        board_id: data.board_id,
        scene: submitScene.snapshot,
        assets: submitScene.assets,
        notes,
        tool_mode: data.tool_mode,
        selection_summary: selectionSummary,
        continued_from_revision_id: continuedFromRevisionId ?? undefined,
        export_refs: latestExportRefRef.current ? [latestExportRefRef.current] : [],
      },
      completedAt: new Date().toISOString(),
    });
  };

  const handleCancel = () => {
    shouldFlushDraftRef.current = false;
    clearDraft();
    onCancel();
  };

  const handleNotesChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    const nextNotes = event.target.value;
    notesRef.current = nextNotes;
    setNotes(nextNotes);
    scheduleAutosave();
  };

  const handleExportPNG = async () => {
    const editor = editorRef.current;
    if (!editor) {
      return;
    }
    const shapeIDs = Array.from(editor.getCurrentPageShapeIds());
    if (shapeIDs.length === 0) {
      setExportStatus("failed");
      setMessage("Export PNG requires at least one shape on the current board.");
      return;
    }
    try {
      const image = await editor.toImage(shapeIDs, {
        format: "png",
        background: true,
      });
      const filename = buildWhiteboardExportFilename(
        boardID,
        activeRevisionId ?? data?.revision_id,
      );
      const url = window.URL.createObjectURL(image.blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);

      const exportRef: WhiteboardExportRef = {
        name: filename,
        mime_type: image.blob.type || "image/png",
        kind: "png",
        created_at: new Date().toISOString(),
        size_bytes: image.blob.size,
        width: image.width,
        height: image.height,
      };
      latestExportRefRef.current = exportRef;
      setLatestExportRef(exportRef);
      setExportStatus("done");
      setMessage(null);
    } catch {
      setExportStatus("failed");
      setMessage("PNG export failed. Try again after the board finishes rendering.");
    }
  };

  const handlePreviewRevision = (revision: WhiteboardRevisionSnapshotItem) => {
    const nextSeed = buildWhiteboardSeedState({
      scene: normalizeSnapshot(revision.scene),
      assetRefs: mergeWhiteboardAssetRefs(revision.assets ?? [], revision.reference_images ?? []),
      notes: revision.notes ?? "",
      revisionId: readRevisionID(revision.revision_id),
    });
    clearDraft();
    setActiveAssetRefs(
      mergeWhiteboardAssetRefs(revision.assets ?? [], revision.reference_images ?? []),
    );
    notesRef.current = nextSeed.notes;
    setNotes(nextSeed.notes);
    setEditorSeed(nextSeed.scene);
    setEditorSeedKey(buildEditorSeedKey(nextSeed.revisionId, `preview-${Date.now()}`));
    setSelectedRevisionId(nextSeed.revisionId);
    setActiveRevisionId(nextSeed.revisionId);
    setContinuedFromRevisionId(null);
    setRevisionMode("preview");
    canonicalSceneRef.current = nextSeed.canonicalScene;
    canonicalNotesRef.current = nextSeed.canonicalNotes;
    canonicalRevisionIdRef.current = nextSeed.revisionId;
    canonicalSeedKeyRef.current = nextSeed.canonicalSeedKey;
    lastPersistedPayloadRef.current = null;
    latestExportRefRef.current = null;
    setLatestExportRef(null);
    setExportStatus("idle");
    setAttemptBlock(null);
    setMessage(
      `Previewing revision ${revision.revision_id}. Continue from here to branch a new revision.`,
    );
  };

  const handleContinueFromRevision = (revision: WhiteboardRevisionSnapshotItem) => {
    const nextSeed = buildWhiteboardSeedState({
      scene: normalizeSnapshot(revision.scene),
      assetRefs: mergeWhiteboardAssetRefs(revision.assets ?? [], revision.reference_images ?? []),
      notes: revision.notes ?? "",
      revisionId: readRevisionID(revision.revision_id),
    });
    const isLatest = nextSeed.revisionId === latestRevisionID;
    clearDraft();
    setActiveAssetRefs(
      mergeWhiteboardAssetRefs(revision.assets ?? [], revision.reference_images ?? []),
    );
    notesRef.current = nextSeed.notes;
    setNotes(nextSeed.notes);
    setEditorSeed(nextSeed.scene);
    setEditorSeedKey(buildEditorSeedKey(nextSeed.revisionId, `continue-${Date.now()}`));
    setSelectedRevisionId(nextSeed.revisionId);
    setActiveRevisionId(nextSeed.revisionId);
    setContinuedFromRevisionId(isLatest ? null : nextSeed.revisionId);
    setRevisionMode(isLatest ? "latest" : "continue");
    canonicalSceneRef.current = nextSeed.canonicalScene;
    canonicalNotesRef.current = nextSeed.canonicalNotes;
    canonicalRevisionIdRef.current = nextSeed.revisionId;
    canonicalSeedKeyRef.current = nextSeed.canonicalSeedKey;
    lastPersistedPayloadRef.current = null;
    latestExportRefRef.current = null;
    setLatestExportRef(null);
    setExportStatus("idle");
    setAttemptBlock(null);
    setMessage(
      isLatest
        ? null
        : `Continuing from revision ${revision.revision_id}. The next submit will append a new revision.`,
    );
  };

  const handleReturnToLatest = () => {
    const nextSeed = buildWhiteboardSeedState({
      scene: initialState.canonicalScene,
      assetRefs,
      notes: initialState.canonicalNotes,
      revisionId: initialState.revisionId,
    });
    clearDraft();
    setActiveAssetRefs(assetRefs);
    notesRef.current = nextSeed.notes;
    setNotes(nextSeed.notes);
    setEditorSeed(nextSeed.scene);
    setEditorSeedKey(buildEditorSeedKey(nextSeed.revisionId, `latest-${Date.now()}`));
    setSelectedRevisionId(nextSeed.revisionId);
    setActiveRevisionId(nextSeed.revisionId);
    setContinuedFromRevisionId(null);
    setRevisionMode("latest");
    canonicalSceneRef.current = nextSeed.canonicalScene;
    canonicalNotesRef.current = nextSeed.canonicalNotes;
    canonicalRevisionIdRef.current = nextSeed.revisionId;
    canonicalSeedKeyRef.current = nextSeed.canonicalSeedKey;
    lastPersistedPayloadRef.current = null;
    latestExportRefRef.current = null;
    setLatestExportRef(null);
    setExportStatus("idle");
    setAttemptBlock(null);
    setMessage(null);
  };

  return (
    <Card data-testid="whiteboard-root" className="w-full max-w-7xl">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">{envelope.title ?? data?.title ?? "Whiteboard"}</CardTitle>
          {envelope.context ? (
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
          {data?.intent ? <p className="text-sm text-zinc-300">{data.intent}</p> : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            board {data?.board_id ?? "unknown"}
          </span>
          {data?.tool_mode ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">{data.tool_mode}</span>
          ) : null}
          {data?.revision_id ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              rev {activeRevisionId ?? data.revision_id}
            </span>
          ) : null}
          {continuedFromRevisionId ? (
            <span className="rounded-full border border-amber-700 px-2 py-1 text-amber-300">
              continuing from {continuedFromRevisionId}
            </span>
          ) : null}
          {data?.updated_at ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              saved {data.updated_at}
            </span>
          ) : null}
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            {data?.assets?.length ?? 0} asset refs
          </span>
          {(data?.export_refs?.length ?? 0) > 0 ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              {data?.export_refs?.length} persisted export
              {data?.export_refs?.length === 1 ? "" : "s"}
            </span>
          ) : null}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {/*
          Every other draft-bearing workflow tells the operator when it restored
          one; this board silently resumed a recovered scene, which is the worst
          case for a canvas whose content is not obviously "unsent".
        */}
        {initialState.recoveredDraft ? (
          <p
            className="text-sm text-emerald-300"
            role="status"
            aria-live="polite"
            data-testid="whiteboard-draft-recovered"
          >
            Recovered unsent whiteboard edits — scene and notes — from this browser.
          </p>
        ) : null}
        {message ? (
          <div
            className="rounded-lg border border-amber-700/60 bg-amber-950/40 px-3 py-2 text-sm text-amber-200"
            role="status"
            aria-live="polite"
            data-testid="whiteboard-message"
          >
            {message}
          </div>
        ) : null}
        <div
          id={CANVAS_ID}
          tabIndex={-1}
          className="h-[640px] overflow-hidden rounded-xl border border-zinc-800 bg-white"
        >
          <Tldraw
            key={editorSeedKey}
            snapshot={editorSeed}
            onMount={(editor) => {
              editorRef.current = editor;
              if (!canonicalSceneRef.current && !initialState.recoveredDraft) {
                canonicalSceneRef.current = getSnapshot(editor.store);
              }
              unsubscribeRef.current?.();
              unsubscribeRef.current = editor.store.listen(
                () => {
                  invalidateExport();
                  clearAttemptBlock();
                  scheduleAutosave();
                },
                { source: "user", scope: "document" },
              );
            }}
          />
        </div>

        <section className="space-y-2">
          <label
            htmlFor={NOTES_ID}
            className="block text-xs font-medium uppercase tracking-wide text-zinc-500"
          >
            Notes
          </label>
          <Textarea
            id={NOTES_ID}
            value={notes}
            onChange={handleNotesChange}
            placeholder="Optional context for the next whiteboard revision"
            rows={4}
            aria-describedby={`${NOTES_ID}-hint`}
            data-testid="whiteboard-notes"
          />
          <FieldMessage id={`${NOTES_ID}-hint`}>
            Optional. Reopening a snapshot, continuing from a revision, or returning to latest
            replaces these notes with that revision's own.
          </FieldMessage>
        </section>

        {revisions.length > 0 ? (
          <section className="space-y-3 rounded-xl border border-zinc-800 bg-zinc-950/40 p-4">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Revisions</p>
              {revisionMode !== "latest" ? (
                <Button type="button" variant="ghost" onClick={handleReturnToLatest}>
                  Return to latest
                </Button>
              ) : null}
            </div>
            <div className="space-y-2" data-testid="whiteboard-revision-browser">
              {revisions
                .slice()
                .reverse()
                .map((revision) => {
                  const isSelected = selectedRevisionId === revision.revision_id;
                  const isActive = activeRevisionId === revision.revision_id;
                  return (
                    <div
                      key={revision.revision_id}
                      className="rounded-lg border border-zinc-800 bg-zinc-900/60 p-3"
                    >
                      <div className="flex flex-wrap items-center gap-2 text-sm text-zinc-200">
                        <span className="font-medium">{revision.revision_id}</span>
                        {isActive ? (
                          <span className="rounded-full border border-emerald-700 px-2 py-0.5 text-[11px] text-emerald-300">
                            open
                          </span>
                        ) : null}
                        {revision.continued_from_revision_id ? (
                          <span className="rounded-full border border-amber-700 px-2 py-0.5 text-[11px] text-amber-300">
                            from {revision.continued_from_revision_id}
                          </span>
                        ) : null}
                      </div>
                      <p className="mt-1 text-xs text-zinc-400">
                        {revision.updated_at ? `saved ${revision.updated_at}` : "saved"}
                        {revision.scene_size ? ` • ${revision.scene_size} scene records` : ""}
                        {revision.asset_count ? ` • ${revision.asset_count} asset refs` : ""}
                      </p>
                      {revision.summary ? (
                        <p className="mt-1 text-sm text-zinc-300">{revision.summary}</p>
                      ) : null}
                      <div className="mt-3 flex flex-wrap gap-2">
                        <Button
                          type="button"
                          variant={isSelected ? "default" : "outline"}
                          onClick={() => {
                            setSelectedRevisionId(revision.revision_id);
                            handlePreviewRevision(revision);
                          }}
                          id={`whiteboard-preview-${revision.revision_id}`}
                          data-testid={`whiteboard-preview-${revision.revision_id}`}
                        >
                          Reopen snapshot
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() => handleContinueFromRevision(revision)}
                          id={`whiteboard-continue-${revision.revision_id}`}
                          data-testid={`whiteboard-continue-${revision.revision_id}`}
                        >
                          Continue from here
                        </Button>
                      </div>
                    </div>
                  );
                })}
            </div>
          </section>
        ) : null}
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        {/*
          A standing autosave notice, not an explanation of a disabled CTA — the
          gate notice below owns that, and the two must not be confused.
        */}
        <p className="mr-auto text-xs text-zinc-500" data-testid="whiteboard-autosave-notice">
          Draft autosaves stay in this browser until you submit or cancel.
          {latestExportRef ? ` Latest PNG export: ${latestExportRef.name}.` : ""}
        </p>
        <SubmitGateNotice
          gate={gate}
          testID="whiteboard-submit-gate"
          action="Submit board"
          onReveal={revealRequirement}
        />
        <Button
          type="button"
          variant="outline"
          onClick={handleExportPNG}
          data-testid="whiteboard-export-png"
        >
          Export PNG
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={handleCancel}
          data-testid="whiteboard-cancel"
        >
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          data-testid="whiteboard-submit"
          disabled={revisionMode === "preview"}
          aria-describedby={gate.blocked ? "whiteboard-submit-gate" : undefined}
        >
          Submit board
        </Button>
      </CardFooter>
      {/*
        One export outcome, announced once. The two branches used to be separate
        unannounced blocks whose wording did not match the footer's own
        "Latest PNG export" line.
      */}
      {exportStatus !== "idle" ? (
        <div
          className={cn(
            "px-6 pb-4 text-xs",
            exportStatus === "done" ? "text-emerald-400" : "text-red-400",
          )}
          role="status"
          aria-live="polite"
          data-testid="whiteboard-export-status"
        >
          {exportStatus === "done"
            ? `PNG exported${latestExportRef ? `: ${latestExportRef.name}` : ""}`
            : "Export failed"}
        </div>
      ) : null}
    </Card>
  );
}

function normalizeSnapshot(scene: WhiteboardEnvelopeData["scene"]): TLEditorSnapshot | undefined {
  if (!scene || typeof scene !== "object") {
    return undefined;
  }
  return scene as TLEditorSnapshot;
}

function readRevisionID(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function resolveInitialWhiteboardState(input: {
  roomID?: string;
  boardID: string;
  envelopeId: string;
  scene?: TLEditorSnapshot;
  assetRefs: WhiteboardAssetRef[];
  notes: string;
  revisionId: string | null;
}): {
  scene?: TLEditorSnapshot;
  notes: string;
  canonicalScene?: TLEditorSnapshot;
  canonicalNotes: string;
  canonicalSeedKey: string;
  revisionId: string | null;
  recoveredDraft: boolean;
  staleReferenceImages: WhiteboardAssetRef[];
} {
  const canonical = buildWhiteboardSeedState({
    scene: input.scene,
    assetRefs: input.assetRefs,
    notes: input.notes,
    revisionId: input.revisionId,
  });
  if (!input.roomID || !input.boardID) {
    return {
      scene: canonical.scene,
      notes: input.notes,
      canonicalScene: canonical.canonicalScene,
      canonicalNotes: input.notes,
      canonicalSeedKey: canonical.canonicalSeedKey,
      revisionId: canonical.revisionId,
      recoveredDraft: false,
      staleReferenceImages: canonical.staleReferenceImages,
    };
  }

  const draft = loadWhiteboardDraft(input.roomID, input.boardID);
  if (!draft) {
    return {
      scene: canonical.scene,
      notes: input.notes,
      canonicalScene: canonical.canonicalScene,
      canonicalNotes: input.notes,
      canonicalSeedKey: canonical.canonicalSeedKey,
      revisionId: canonical.revisionId,
      recoveredDraft: false,
      staleReferenceImages: canonical.staleReferenceImages,
    };
  }

  if (
    !isCompatibleDraft(draft, {
      envelopeId: input.envelopeId,
      revisionId: input.revisionId,
      canonicalSeedKey: canonical.canonicalSeedKey,
    })
  ) {
    clearWhiteboardDraft(input.roomID, input.boardID);
    return {
      scene: canonical.scene,
      notes: input.notes,
      canonicalScene: canonical.canonicalScene,
      canonicalNotes: input.notes,
      canonicalSeedKey: canonical.canonicalSeedKey,
      revisionId: canonical.revisionId,
      recoveredDraft: false,
      staleReferenceImages: canonical.staleReferenceImages,
    };
  }

  return {
    scene: prepareWhiteboardSnapshotForEditor(draft.scene, input.assetRefs).snapshot,
    notes: draft.notes,
    canonicalScene: canonical.canonicalScene,
    canonicalNotes: input.notes,
    canonicalSeedKey: canonical.canonicalSeedKey,
    revisionId: canonical.revisionId,
    recoveredDraft: true,
    staleReferenceImages: canonical.staleReferenceImages,
  };
}

function buildWhiteboardSeedState(input: {
  scene?: TLEditorSnapshot;
  assetRefs: WhiteboardAssetRef[];
  notes: string;
  revisionId: string | null;
}): {
  scene?: TLEditorSnapshot;
  notes: string;
  canonicalScene?: TLEditorSnapshot;
  canonicalNotes: string;
  canonicalSeedKey: string;
  revisionId: string | null;
  staleReferenceImages: WhiteboardAssetRef[];
} {
  const hydratedCanonical = prepareWhiteboardSnapshotForEditor(input.scene, input.assetRefs);
  return {
    scene: hydratedCanonical.snapshot,
    notes: input.notes,
    canonicalScene: hydratedCanonical.snapshot,
    canonicalNotes: input.notes,
    canonicalSeedKey: buildWhiteboardCanonicalSeedKey({
      notes: input.notes,
      scene: hydratedCanonical.snapshot,
    }),
    revisionId: input.revisionId,
    staleReferenceImages: hydratedCanonical.staleReferenceImages,
  };
}

function isCompatibleDraft(
  draft: {
    envelopeId: string;
    baseRevisionId: string | null;
    baseSeedKey: string;
  },
  canonical: {
    envelopeId: string;
    revisionId: string | null;
    canonicalSeedKey: string;
  },
): boolean {
  if (draft.baseRevisionId || canonical.revisionId) {
    return draft.baseRevisionId === canonical.revisionId;
  }
  return (
    draft.envelopeId === canonical.envelopeId && draft.baseSeedKey === canonical.canonicalSeedKey
  );
}

function summarizeSelection(editor: Editor): WhiteboardSelectionSummary | undefined {
  const ids = editor.getSelectedShapeIds();
  if (ids.length === 0) {
    return undefined;
  }
  const types = Array.from(
    new Set(
      editor
        .getSelectedShapes()
        .map((shape) => shape.type)
        .filter((type): type is typeof type => typeof type === "string" && type.length > 0),
    ),
  );
  return {
    count: ids.length,
    ids: ids.map((id) => String(id)),
    types: types.length > 0 ? types : undefined,
  };
}

function buildWhiteboardExportFilename(boardID: string, revisionID?: string): string {
  const safeBoardID = boardID.trim().length > 0 ? boardID.trim() : "whiteboard";
  const safeRevisionID =
    typeof revisionID === "string" && revisionID.trim().length > 0 ? revisionID.trim() : "draft";
  return `${safeBoardID}-${safeRevisionID}.png`;
}

function buildStaleReferenceImageMessage(count: number): string {
  return count === 1
    ? "1 persisted reference image could not be reloaded from its artifact ref."
    : `${count} persisted reference images could not be reloaded from their artifact refs.`;
}

function buildEditorSeedKey(revisionId: string | null, suffix: string): string {
  return `${revisionId ?? "draft"}:${suffix}`;
}
