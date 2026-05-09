import { type ChangeEvent, useEffect, useEffectEvent, useRef, useState } from "react";
import { type Editor, getSnapshot, type TLEditorSnapshot, Tldraw } from "tldraw";
import "tldraw/tldraw.css";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import {
  buildWhiteboardCanonicalSeedKey,
  clearWhiteboardDraft,
  loadWhiteboardDraft,
  saveWhiteboardDraft,
  WHITEBOARD_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/whiteboard-draft-storage";

export interface WhiteboardAssetRef {
  asset_id?: string;
  artifact_id?: string;
  name?: string;
  mime_type?: string;
  source?: string;
  width?: number;
  height?: number;
}

export interface WhiteboardRevisionHistoryItem {
  revision_id: string;
  updated_at?: string;
  summary?: string;
  scene_size?: number;
  asset_count?: number;
}

export interface WhiteboardSelectionSummary {
  count: number;
  ids?: string[];
  types?: string[];
}

export interface WhiteboardExportRef {
  artifact_id?: string;
  name?: string;
  mime_type?: string;
  kind?: string;
  uri?: string;
}

export interface WhiteboardEnvelopeData {
  board_id: string;
  title?: string;
  intent?: string;
  scene?: TLEditorSnapshot | Record<string, unknown>;
  assets?: WhiteboardAssetRef[];
  notes?: string;
  updated_at?: string;
  revision_id?: string;
  revision_history?: WhiteboardRevisionHistoryItem[];
  tool_mode?: "select" | "draw" | "text" | "shape" | "arrow" | "note";
  reference_images?: Array<Record<string, unknown>>;
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

export function Whiteboard({ envelope, onSubmit, onCancel, roomID }: WhiteboardProps) {
  const data = envelope.data;
  const editorRef = useRef<Editor | null>(null);
  const boardID = data?.board_id ?? "";
  const [initialState] = useState(() =>
    resolveInitialWhiteboardState({
      roomID,
      boardID,
      envelopeId: envelope.id,
      scene: normalizeSnapshot(data?.scene),
      notes: data?.notes ?? "",
      revisionId: readRevisionID(data?.revision_id),
    }),
  );
  const [notes, setNotes] = useState(initialState.notes);
  const notesRef = useRef(initialState.notes);
  const autosaveTimerRef = useRef<number | null>(null);
  const unsubscribeRef = useRef<(() => void) | null>(null);
  const shouldFlushDraftRef = useRef(Boolean(roomID && boardID));
  const canonicalSceneRef = useRef<TLEditorSnapshot | undefined>(initialState.canonicalScene);
  const canonicalNotesRef = useRef(initialState.canonicalNotes);
  const canonicalRevisionIdRef = useRef(initialState.revisionId);
  const canonicalSeedKeyRef = useRef(initialState.canonicalSeedKey);
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

  const handleSubmit = () => {
    const editor = editorRef.current;
    if (!editor || !data?.board_id) {
      return;
    }
    const selectionSummary = summarizeSelection(editor);
    shouldFlushDraftRef.current = false;
    clearDraft();
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        board_id: data.board_id,
        scene: getSnapshot(editor.store),
        assets: data.assets ?? [],
        notes,
        tool_mode: data.tool_mode,
        selection_summary: selectionSummary,
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
              rev {data.revision_id}
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
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        <div className="h-[640px] overflow-hidden rounded-xl border border-zinc-800 bg-white">
          <Tldraw
            snapshot={initialState.scene}
            onMount={(editor) => {
              editorRef.current = editor;
              if (!canonicalSceneRef.current && !initialState.recoveredDraft) {
                canonicalSceneRef.current = getSnapshot(editor.store);
              }
              unsubscribeRef.current?.();
              unsubscribeRef.current = editor.store.listen(
                () => {
                  scheduleAutosave();
                },
                { source: "user", scope: "document" },
              );
            }}
          />
        </div>

        <section className="space-y-2">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Notes</p>
          <Textarea
            value={notes}
            onChange={handleNotesChange}
            placeholder="Add context for the next whiteboard revision."
            rows={4}
            data-testid="whiteboard-notes"
          />
        </section>
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <p className="mr-auto text-xs text-zinc-500">
          Draft autosaves stay in this browser until you submit or cancel.
        </p>
        <Button
          type="button"
          variant="ghost"
          onClick={handleCancel}
          data-testid="whiteboard-cancel"
        >
          Cancel
        </Button>
        <Button type="button" onClick={handleSubmit} data-testid="whiteboard-submit">
          Submit board
        </Button>
      </CardFooter>
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
} {
  const canonicalSeedKey = buildWhiteboardCanonicalSeedKey({
    notes: input.notes,
    scene: input.scene,
  });
  if (!input.roomID || !input.boardID) {
    return {
      scene: input.scene,
      notes: input.notes,
      canonicalScene: input.scene,
      canonicalNotes: input.notes,
      canonicalSeedKey,
      revisionId: input.revisionId,
      recoveredDraft: false,
    };
  }

  const draft = loadWhiteboardDraft(input.roomID, input.boardID);
  if (!draft) {
    return {
      scene: input.scene,
      notes: input.notes,
      canonicalScene: input.scene,
      canonicalNotes: input.notes,
      canonicalSeedKey,
      revisionId: input.revisionId,
      recoveredDraft: false,
    };
  }

  if (
    !isCompatibleDraft(draft, {
      envelopeId: input.envelopeId,
      revisionId: input.revisionId,
      canonicalSeedKey,
    })
  ) {
    clearWhiteboardDraft(input.roomID, input.boardID);
    return {
      scene: input.scene,
      notes: input.notes,
      canonicalScene: input.scene,
      canonicalNotes: input.notes,
      canonicalSeedKey,
      revisionId: input.revisionId,
      recoveredDraft: false,
    };
  }

  return {
    scene: draft.scene,
    notes: draft.notes,
    canonicalScene: input.scene,
    canonicalNotes: input.notes,
    canonicalSeedKey,
    revisionId: input.revisionId,
    recoveredDraft: true,
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
