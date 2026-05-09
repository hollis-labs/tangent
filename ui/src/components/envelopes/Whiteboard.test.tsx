import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  getWhiteboardDraftStorageKey,
  WHITEBOARD_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/whiteboard-draft-storage";
import { Whiteboard, type WhiteboardEnvelope } from "./Whiteboard";

type MockSnapshot = {
  document: { pages: Array<{ id: string }> };
  session: { currentPageId: string };
  [key: string]: unknown;
};

const getSnapshot = vi.fn(() => ({
  document: { pages: [{ id: "page:live" }] },
  session: { currentPageId: "page:live" },
}));
const blankSnapshot: MockSnapshot = {
  document: { pages: [{ id: "page:blank" }] },
  session: { currentPageId: "page:blank" },
};
let currentSnapshot: MockSnapshot = blankSnapshot;
let listeners: Array<() => void> = [];

vi.mock("tldraw", () => ({
  Tldraw: ({
    snapshot,
    onMount,
  }: {
    snapshot?: unknown;
    onMount?: (editor: {
      store: {
        listen: (listener: () => void) => () => void;
      };
      getSelectedShapeIds: () => string[];
      getSelectedShapes: () => Array<{ type: string }>;
    }) => void;
  }) => {
    currentSnapshot = (snapshot as MockSnapshot | undefined) ?? blankSnapshot;
    onMount?.({
      store: {
        listen: (listener: () => void) => {
          listeners.push(listener);
          return () => {
            listeners = listeners.filter((entry) => entry !== listener);
          };
        },
      },
      getSelectedShapeIds: () => ["shape:1"],
      getSelectedShapes: () => [{ type: "geo" }],
    });
    return <div data-testid="tldraw-host" data-snapshot={JSON.stringify(snapshot ?? null)} />;
  },
  getSnapshot: () => getSnapshot(),
}));

const baseEnvelope: WhiteboardEnvelope = {
  v: 1,
  id: "whiteboard-1",
  type: "tangent.whiteboard",
  title: "Whiteboard",
  data: {
    board_id: "board-1",
    intent: "Map the layout",
    scene: {
      document: { pages: [{ id: "page:1" }] },
      session: { currentPageId: "page:1" },
    },
    assets: [{ artifact_id: "artifact-1", source: "artifact://artifact-1" }],
    notes: "seed notes",
    tool_mode: "draw",
  },
};

describe("Whiteboard", () => {
  beforeEach(() => {
    getSnapshot.mockClear();
    getSnapshot.mockImplementation(() => currentSnapshot);
    currentSnapshot = blankSnapshot;
    listeners = [];
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads a seeded board snapshot", () => {
    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toContain("page:1");
    expect(screen.getByDisplayValue("seed notes")).toBeInTheDocument();
  });

  it("loads a blank board when no snapshot is provided", () => {
    render(
      <Whiteboard
        envelope={{
          ...baseEnvelope,
          data: { ...baseEnvelope.data, board_id: "board-1", scene: undefined, notes: "" },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toBe("null");
  });

  it("submits a full scene snapshot response", () => {
    const onSubmit = vi.fn();
    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.change(screen.getByTestId("whiteboard-notes"), {
      target: { value: "updated notes" },
    });
    fireEvent.click(screen.getByTestId("whiteboard-submit"));

    expect(getSnapshot).toHaveBeenCalled();
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      envelopeId: "whiteboard-1",
      kind: "data",
      status: "submitted",
      payload: {
        board_id: "board-1",
        notes: "updated notes",
        selection_summary: {
          count: 1,
          ids: ["shape:1"],
          types: ["geo"],
        },
      },
    });
  });

  it("cancel delegates without emitting a submit payload", () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(
      <Whiteboard
        envelope={baseEnvelope}
        onSubmit={onSubmit}
        onCancel={onCancel}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("whiteboard-cancel"));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("debounces host-local draft writes while the envelope is open", () => {
    vi.useFakeTimers();
    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.change(screen.getByTestId("whiteboard-notes"), {
      target: { value: "draft notes" },
    });
    emitDocumentChange({
      document: { pages: [{ id: "page:draft" }] },
      session: { currentPageId: "page:draft" },
    });

    const storageKey = getWhiteboardDraftStorageKey("room-a", "board-1");
    expect(window.localStorage.getItem(storageKey)).toBeNull();

    act(() => {
      vi.advanceTimersByTime(WHITEBOARD_AUTOSAVE_DEBOUNCE_MS - 1);
    });
    expect(window.localStorage.getItem(storageKey)).toBeNull();

    act(() => {
      vi.advanceTimersByTime(1);
    });

    const saved = JSON.parse(window.localStorage.getItem(storageKey) ?? "null");
    expect(saved).toMatchObject({
      roomID: "room-a",
      boardID: "board-1",
      notes: "draft notes",
      baseRevisionId: null,
    });
    expect(saved.scene.document.pages[0].id).toBe("page:draft");
  });

  it("recovers a compatible local draft after a reload", () => {
    vi.useFakeTimers();
    const firstRender = render(
      <Whiteboard envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.change(screen.getByTestId("whiteboard-notes"), {
      target: { value: "recovered draft notes" },
    });
    emitDocumentChange({
      document: { pages: [{ id: "page:recovered" }] },
      session: { currentPageId: "page:recovered" },
    });
    act(() => {
      vi.advanceTimersByTime(WHITEBOARD_AUTOSAVE_DEBOUNCE_MS);
    });

    firstRender.unmount();

    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toContain(
      "page:recovered",
    );
    expect(screen.getByDisplayValue("recovered draft notes")).toBeInTheDocument();
  });

  it("ignores and clears stale drafts after the canonical revision advances", () => {
    const storageKey = getWhiteboardDraftStorageKey("room-a", "board-1");
    window.localStorage.setItem(
      storageKey,
      JSON.stringify({
        version: 1,
        roomID: "room-a",
        boardID: "board-1",
        envelopeId: "whiteboard-1",
        baseRevisionId: "board-1-r1",
        baseSeedKey: '{"notes":"seed notes","scene":null}',
        notes: "stale local notes",
        scene: {
          document: { pages: [{ id: "page:stale-local" }] },
          session: { currentPageId: "page:stale-local" },
        },
        savedAt: "2026-05-09T20:20:00.000Z",
      }),
    );

    render(
      <Whiteboard
        envelope={{
          ...baseEnvelope,
          data: {
            ...baseEnvelope.data,
            board_id: "board-1",
            notes: "canonical notes",
            revision_id: "board-1-r2",
            scene: {
              document: { pages: [{ id: "page:canonical" }] },
              session: { currentPageId: "page:canonical" },
            },
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toContain(
      "page:canonical",
    );
    expect(screen.getByDisplayValue("canonical notes")).toBeInTheDocument();
    expect(window.localStorage.getItem(storageKey)).toBeNull();
  });
});

function emitDocumentChange(snapshot: MockSnapshot) {
  currentSnapshot = snapshot;
  for (const listener of listeners) {
    listener();
  }
}
