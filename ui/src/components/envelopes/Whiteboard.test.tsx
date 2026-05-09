import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  getWhiteboardDraftStorageKey,
  WHITEBOARD_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/whiteboard-draft-storage";
import { Whiteboard, type WhiteboardEnvelope } from "./Whiteboard";

type MockSnapshot = {
  document?: { pages: Array<{ id: string }> };
  session?: { currentPageId: string };
  store?: Record<string, unknown>;
  [key: string]: unknown;
};

const getSnapshot = vi.fn<() => MockSnapshot>(() => ({
  document: { pages: [{ id: "page:live" }] },
  session: { currentPageId: "page:live" },
}));
const toImage = vi.fn(async () => ({
  blob: new Blob(["png"], { type: "image/png" }),
  width: 1200,
  height: 800,
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
      getCurrentPageShapeIds: () => Set<string>;
      toImage: () => Promise<{ blob: Blob; width: number; height: number }>;
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
      getCurrentPageShapeIds: () => new Set(["shape:1"]),
      toImage,
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
    toImage.mockClear();
    currentSnapshot = blankSnapshot;
    listeners = [];
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
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

  it("exports the current board as PNG and carries export metadata on submit", async () => {
    const createObjectURL = vi.fn(() => "blob:png-export");
    const revokeObjectURL = vi.fn();
    Object.defineProperty(window.URL, "createObjectURL", {
      configurable: true,
      writable: true,
      value: createObjectURL,
    });
    Object.defineProperty(window.URL, "revokeObjectURL", {
      configurable: true,
      writable: true,
      value: revokeObjectURL,
    });

    const onSubmit = vi.fn();
    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />,
    );

    await act(async () => {
      fireEvent.click(screen.getByTestId("whiteboard-export-png"));
    });
    expect(toImage).toHaveBeenCalledTimes(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await screen.findByTestId("whiteboard-export-status")).toHaveTextContent("PNG exported");

    fireEvent.click(screen.getByTestId("whiteboard-submit"));
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload.export_refs[0]).toMatchObject({
      kind: "png",
      mime_type: "image/png",
      width: 1200,
      height: 800,
    });
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:png-export");
  });

  it("hydrates artifact-backed reference images onto the editor snapshot", () => {
    const envelope: WhiteboardEnvelope = {
      ...baseEnvelope,
      data: {
        ...(baseEnvelope.data ?? { board_id: "board-1" }),
        scene: {
          store: {
            "asset:image-1": {
              id: "asset:image-1",
              typeName: "asset",
              type: "image",
              props: {
                src: "artifact://artifact-1",
                w: 640,
                h: 480,
                name: "reference.png",
                mimeType: "image/png",
              },
            },
          },
        },
        assets: [],
        reference_images: [
          {
            asset_id: "asset:image-1",
            artifact_id: "artifact-1",
            uri: "artifact://artifact-1",
            source: "https://assets.example.test/reference.png",
            kind: "reference_image",
            mime_type: "image/png",
          },
        ],
      },
    };

    render(
      <Whiteboard envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toContain(
      "https://assets.example.test/reference.png",
    );
  });

  it("blocks submit when the board still contains browser-only image assets", () => {
    const onSubmit = vi.fn();
    getSnapshot.mockImplementation(() => ({
      store: {
        "asset:image-local": {
          id: "asset:image-local",
          typeName: "asset",
          type: "image",
          props: {
            src: "blob:local-image",
            w: 320,
            h: 180,
          },
        },
      },
    }));

    render(
      <Whiteboard envelope={baseEnvelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.click(screen.getByTestId("whiteboard-submit"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByTestId("whiteboard-message")).toHaveTextContent("browser-only image");
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
