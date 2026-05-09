import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { Whiteboard, type WhiteboardEnvelope } from "./Whiteboard";

const getSnapshot = vi.fn(() => ({
  document: { pages: [{ id: "page:live" }] },
  session: { currentPageId: "page:live" },
}));

vi.mock("tldraw", () => ({
  Tldraw: ({
    snapshot,
    onMount,
  }: {
    snapshot?: unknown;
    onMount?: (editor: {
      store: Record<string, unknown>;
      getSelectedShapeIds: () => string[];
      getSelectedShapes: () => Array<{ type: string }>;
    }) => void;
  }) => {
    onMount?.({
      store: { mocked: true },
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
  });

  it("loads a seeded board snapshot", () => {
    render(<Whiteboard envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

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
      />,
    );

    expect(screen.getByTestId("tldraw-host").getAttribute("data-snapshot")).toBe("null");
  });

  it("submits a full scene snapshot response", () => {
    const onSubmit = vi.fn();
    render(<Whiteboard envelope={baseEnvelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByTestId("whiteboard-notes"), {
      target: { value: "updated notes" },
    });
    fireEvent.click(screen.getByTestId("whiteboard-submit"));

    expect(getSnapshot).toHaveBeenCalledTimes(1);
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
    render(<Whiteboard envelope={baseEnvelope} onSubmit={onSubmit} onCancel={onCancel} />);

    fireEvent.click(screen.getByTestId("whiteboard-cancel"));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
