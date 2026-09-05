// Validation-affordance coverage for the whiteboard.
//
// The audit's finding here was distance. "Submit board" is disabled by preview
// mode; the trigger for preview mode ("Reopen snapshot") sits at the bottom of
// the card, the explanation rendered at the very top above a 640px canvas, and
// the button sits in the footer — three regions the operator has to connect for
// themselves. The `handleSubmit` branch that would have written the explanation
// was dead code, because `disabled` prevented the click that reached it.
//
// The other two guards were worse: an unmounted editor returned silently, and
// the unsupported-local-asset check wrote its message 640 pixels above the
// button with no scroll.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { WHITEBOARD_AUTOSAVE_DEBOUNCE_MS } from "@/lib/whiteboard-draft-storage";
import { Whiteboard, type WhiteboardEnvelope } from "./Whiteboard";

type MockSnapshot = {
  document?: { pages: Array<{ id: string }> };
  session?: { currentPageId: string };
  store?: Record<string, unknown>;
  [key: string]: unknown;
};

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
      store: { listen: (listener: () => void) => () => void };
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
      toImage: async () => ({
        blob: new Blob(["png"], { type: "image/png" }),
        width: 10,
        height: 10,
      }),
    });
    return <div data-testid="tldraw-host" data-snapshot={JSON.stringify(snapshot ?? null)} />;
  },
  getSnapshot: () => currentSnapshot,
}));

function envelopeWithRevisions(): WhiteboardEnvelope {
  return {
    v: 1,
    id: "whiteboard-1",
    type: "tangent.whiteboard",
    title: "Whiteboard",
    data: {
      board_id: "board-1",
      scene: {
        document: { pages: [{ id: "page:1" }] },
        session: { currentPageId: "page:1" },
      },
      notes: "seed notes",
      revision_id: "board-1-r2",
      revisions: [
        {
          revision_id: "board-1-r1",
          notes: "first revision",
          scene: {
            document: { pages: [{ id: "page:r1" }] },
            session: { currentPageId: "page:r1" },
          },
        },
        {
          revision_id: "board-1-r2",
          notes: "second revision",
          scene: {
            document: { pages: [{ id: "page:r2" }] },
            session: { currentPageId: "page:r2" },
          },
        },
      ],
    },
  };
}

function emitDocumentChange(snapshot: MockSnapshot) {
  currentSnapshot = snapshot;
  for (const listener of listeners) {
    listener();
  }
}

beforeEach(() => {
  currentSnapshot = blankSnapshot;
  listeners = [];
  window.localStorage.clear();
});

describe("Whiteboard validation affordances", () => {
  it("explains the disabled Submit next to the button and goes to the control that clears it", () => {
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    // No standing notice while the board is submittable.
    expect(screen.queryByTestId("whiteboard-submit-gate-reason")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("whiteboard-preview-board-1-r1"));

    expect(screen.getByTestId("whiteboard-submit")).toBeDisabled();
    const notice = screen.getByTestId("whiteboard-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("whiteboard-submit-gate-reason")).toHaveTextContent(
      "Submit board is disabled: revision board-1-r1 is open for inspection only — continue from it to make edits submittable.",
    );
    expect(screen.getByTestId("whiteboard-submit")).toHaveAttribute(
      "aria-describedby",
      "whiteboard-submit-gate",
    );

    // The notice takes the operator to "Continue from here" at the bottom of
    // the card rather than leaving them to find it below a 640px canvas.
    fireEvent.click(screen.getByTestId("whiteboard-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("whiteboard-continue-board-1-r1"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });

    fireEvent.click(screen.getByTestId("whiteboard-continue-board-1-r1"));
    expect(screen.getByTestId("whiteboard-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("whiteboard-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("surfaces the browser-only asset refusal beside the CTA, not only 640px above it", () => {
    const onSubmit = vi.fn();
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    // A browser-only image asset only exists once the operator drops one on the
    // board, so it arrives through a store change rather than the seed.
    emitDocumentChange({
      document: { pages: [{ id: "page:1" }] },
      session: { currentPageId: "page:1" },
      store: {
        "asset:local": {
          id: "asset:local",
          typeName: "asset",
          type: "image",
          props: { src: "blob:local-image", w: 320, h: 180 },
        },
      },
    });

    fireEvent.click(screen.getByTestId("whiteboard-submit"));

    expect(onSubmit).not.toHaveBeenCalled();
    // The banner keeps its detail where the canvas is...
    expect(screen.getByTestId("whiteboard-message")).toHaveTextContent("browser-only image");
    expect(screen.getByTestId("whiteboard-message")).toHaveAttribute("aria-live", "polite");
    // ...and the CTA now says why the click did nothing.
    expect(screen.getByTestId("whiteboard-submit-gate-reason")).toHaveTextContent(
      "Submit board is disabled: 1 browser-only image asset must be removed from the board first.",
    );
    expect(screen.getByTestId("whiteboard-submit-gate-go")).toHaveTextContent("Go to board");

    // Editing the board clears the recorded refusal.
    act(() => {
      emitDocumentChange({
        document: { pages: [{ id: "page:clean" }] },
        session: { currentPageId: "page:clean" },
      });
    });
    expect(screen.queryByTestId("whiteboard-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("labels the notes field and warns that revision switches replace it", () => {
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    const notes = screen.getByTestId("whiteboard-notes");
    expect(document.querySelector("label[for='whiteboard-notes']")).toHaveTextContent("Notes");
    expect(notes).toHaveAttribute("aria-describedby", "whiteboard-notes-hint");
    expect(document.getElementById("whiteboard-notes-hint")).toHaveTextContent(
      "Optional. Reopening a snapshot, continuing from a revision, or returning to latest replaces these notes with that revision's own.",
    );

    // The replacement itself is unchanged — only the warning is new.
    fireEvent.change(notes, { target: { value: "typed by hand" } });
    fireEvent.click(screen.getByTestId("whiteboard-continue-board-1-r1"));
    expect(screen.getByTestId("whiteboard-notes")).toHaveValue("first revision");
  });

  it("keeps the autosave footer distinct from the gate explanation", () => {
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    // The autosave line is standing information and stays put; the gate notice
    // is the only thing that ever explains a dead button.
    expect(screen.getByTestId("whiteboard-autosave-notice")).toHaveTextContent(
      "Draft autosaves stay in this browser until you submit or cancel.",
    );
    expect(screen.queryByTestId("whiteboard-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("announces the export outcome once, in one place", async () => {
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("whiteboard-export-png"));

    const status = await screen.findByTestId("whiteboard-export-status");
    expect(status).toHaveAttribute("role", "status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveTextContent("PNG exported: board-1");
    expect(screen.getAllByTestId("whiteboard-export-status")).toHaveLength(1);
  });

  it("tells the operator when it resumed a recovered draft", () => {
    vi.useFakeTimers();
    const first = render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );
    // Nothing to recover on a first open.
    expect(screen.queryByTestId("whiteboard-draft-recovered")).not.toBeInTheDocument();

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
    first.unmount();
    vi.useRealTimers();

    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    const banner = screen.getByTestId("whiteboard-draft-recovered");
    expect(banner).toHaveAttribute("role", "status");
    expect(banner).toHaveTextContent(
      "Recovered unsent whiteboard edits — scene and notes — from this browser.",
    );
    expect(screen.getByTestId("whiteboard-notes")).toHaveValue("recovered draft notes");
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = vi.fn();
    render(
      <Whiteboard
        envelope={envelopeWithRevisions()}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.change(screen.getByTestId("whiteboard-notes"), { target: { value: "final notes" } });
    fireEvent.click(screen.getByTestId("whiteboard-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload.board_id).toBe("board-1");
    expect(response.payload.notes).toBe("final notes");
    expect(response.payload.assets).toEqual([]);
    expect(response.payload.continued_from_revision_id).toBeUndefined();
    expect(response.payload.export_refs).toEqual([]);
    expect(response.payload.selection_summary).toEqual({
      count: 1,
      ids: ["shape:1"],
      types: ["geo"],
    });
  });
});
