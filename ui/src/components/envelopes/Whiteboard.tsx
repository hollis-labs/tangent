import { useRef, useState } from "react";
import { type Editor, getSnapshot, type TLEditorSnapshot, Tldraw } from "tldraw";
import "tldraw/tldraw.css";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";

export interface WhiteboardAssetRef {
  asset_id?: string;
  artifact_id?: string;
  name?: string;
  mime_type?: string;
  source?: string;
  width?: number;
  height?: number;
}

export interface WhiteboardEnvelopeData {
  board_id: string;
  title?: string;
  intent?: string;
  scene?: TLEditorSnapshot | Record<string, unknown>;
  assets?: WhiteboardAssetRef[];
  notes?: string;
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

export interface WhiteboardResponse {
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
  };
  completedAt: string;
}

export type WhiteboardProps = {
  envelope: WhiteboardEnvelope;
  onSubmit: (response: WhiteboardResponse) => void;
  onCancel: () => void;
};

export function Whiteboard({ envelope, onSubmit, onCancel }: WhiteboardProps) {
  const data = envelope.data;
  const editorRef = useRef<Editor | null>(null);
  const [notes, setNotes] = useState(data?.notes ?? "");

  const handleSubmit = () => {
    const editor = editorRef.current;
    if (!editor || !data?.board_id) {
      return;
    }
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
      },
      completedAt: new Date().toISOString(),
    });
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
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            {data?.assets?.length ?? 0} asset refs
          </span>
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        <div className="h-[640px] overflow-hidden rounded-xl border border-zinc-800 bg-white">
          <Tldraw
            snapshot={normalizeSnapshot(data?.scene)}
            onMount={(editor) => {
              editorRef.current = editor;
            }}
          />
        </div>

        <section className="space-y-2">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Notes</p>
          <Textarea
            value={notes}
            onChange={(event) => setNotes(event.target.value)}
            placeholder="Add context for the next whiteboard revision."
            rows={4}
            data-testid="whiteboard-notes"
          />
        </section>
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="whiteboard-cancel">
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
