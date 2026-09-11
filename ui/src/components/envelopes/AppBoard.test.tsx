import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import {
  AppBoard,
  type AppBoardEnvelope,
  applyFilters,
  applyStagedColumns,
  type BoardCardData,
  CARD_DRAG_TYPE,
  isPluginRoutePath,
} from "./AppBoard";

const cards: BoardCardData[] = [
  {
    id: "CW-1",
    title: "Fix the transport",
    subtitle: "tether",
    badges: [{ label: "p2", tone: "warning" }],
    body: "The **switch case** is the whole change.",
    fields: { status: "doing", tag: "transport" },
  },
  {
    id: "CW-2",
    title: "Ship the board",
    subtitle: "tangent",
    fields: { status: "todo", tag: "ui" },
  },
  {
    id: "CW-3",
    title: "Write the glue test",
    fields: { status: "todo", tag: "transport" },
  },
];

function envelope(
  overrides: Partial<NonNullable<AppBoardEnvelope["data"]>> = {},
): AppBoardEnvelope {
  return {
    v: 1,
    id: "board-env-1",
    type: "tangent.app-board",
    title: "Active work",
    data: {
      board_id: "board-1",
      title: "Active work",
      source: { app: "torque", label: "Torque · active" },
      cards,
      columns: [
        { id: "todo", label: "Todo", card_ids: ["CW-2", "CW-3"] },
        { id: "doing", label: "Doing", card_ids: ["CW-1"] },
      ],
      filters: [
        {
          id: "status",
          label: "Status",
          kind: "multi",
          options: [
            { value: "todo", label: "Todo", count: 2 },
            { value: "doing", label: "Doing", count: 1 },
          ],
        },
      ],
      ...overrides,
    },
  };
}

describe("applyFilters", () => {
  // This is the "filters are a view, not a query" claim, checked directly.
  // It is the sentence a future reader is most likely to assume the other way.
  it("narrows the supplied cards and can never reach one that was not supplied", () => {
    const filters = envelope().data?.filters ?? [];
    expect(applyFilters(cards, filters, {}).map((c) => c.id)).toEqual(["CW-1", "CW-2", "CW-3"]);
    expect(applyFilters(cards, filters, { status: ["todo"] }).map((c) => c.id)).toEqual([
      "CW-2",
      "CW-3",
    ]);
    // Selecting a value no supplied card carries yields nothing — it does not
    // go and look for one.
    expect(applyFilters(cards, filters, { status: ["archived"] })).toEqual([]);
  });

  it("ORs within one filter and ANDs across filters", () => {
    const filters = [
      { id: "status", label: "Status", kind: "multi" as const },
      { id: "tag", label: "Tag", kind: "multi" as const },
    ];
    expect(applyFilters(cards, filters, { status: ["todo", "doing"] }).map((c) => c.id)).toEqual([
      "CW-1",
      "CW-2",
      "CW-3",
    ]);
    expect(
      applyFilters(cards, filters, { status: ["todo"], tag: ["transport"] }).map((c) => c.id),
    ).toEqual(["CW-3"]);
  });

  it("matches a text filter against title, subtitle and body", () => {
    const filters = [{ id: "q", label: "Search", kind: "text" as const }];
    expect(applyFilters(cards, filters, { q: ["transport"] }).map((c) => c.id)).toEqual(["CW-1"]);
    expect(applyFilters(cards, filters, { q: ["tangent"] }).map((c) => c.id)).toEqual(["CW-2"]);
  });

  // The body renders as markdown, but the filter runs on the markdown SOURCE,
  // which is the string the caller sent and the only one it can predict. A
  // filter that matched rendered text instead would silently stop finding
  // anything a caller had emphasized.
  it("matches the markdown source, not the rendered text", () => {
    const filters = [{ id: "q", label: "Search", kind: "text" as const }];
    expect(applyFilters(cards, filters, { q: ["**switch case**"] }).map((c) => c.id)).toEqual([
      "CW-1",
    ]);
  });
});

describe("<AppBoard>", () => {
  it("renders columns, cards and the caller's source label", () => {
    render(<AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("app-board-source")).toHaveTextContent("Torque · active");
    expect(screen.getByTestId("app-board-column-todo")).toBeInTheDocument();
    expect(screen.getByTestId("app-board-column-doing")).toBeInTheDocument();
    expect(screen.getByTestId("app-board-card-CW-1")).toHaveTextContent("Fix the transport");
    // Card bodies go through the shared markdown renderer, so emphasis is an
    // element rather than literal asterisks.
    expect(screen.getByTestId("app-board-card-CW-1").querySelector("strong")).toHaveTextContent(
      "switch case",
    );
  });

  it("drops a column card_id naming a card the caller did not supply", () => {
    const view = envelope({
      columns: [{ id: "todo", label: "Todo", card_ids: ["CW-2", "GHOST-9"] }],
    });
    render(<AppBoard envelope={view} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByTestId("app-board-card-CW-2")).toBeInTheDocument();
    expect(screen.queryByTestId("app-board-card-GHOST-9")).toBeNull();
    // The count reflects what is actually there, not what was referenced.
    expect(screen.getByTestId("app-board-column-todo")).toHaveTextContent("Todo1");
  });

  it("says out loud that filters narrow the supplied set", () => {
    render(<AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByTestId("app-board-scope")).toHaveTextContent(
      "Filters narrow what the caller sent",
    );
  });

  // The draft path. These are the SPA half of what CW-20260909-0046 asserts on
  // the server: view state leaves through onDraft, and never through onSubmit.
  it("publishes a draft when a filter is toggled, and does not submit", () => {
    const onSubmit = vi.fn();
    const onDraft = vi.fn();
    render(
      <AppBoard envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} onDraft={onDraft} />,
    );

    fireEvent.click(screen.getByRole("button", { name: /Todo 2/ }));

    expect(onDraft).toHaveBeenCalledTimes(1);
    expect(onDraft.mock.calls[0][0]).toMatchObject({
      board_id: "board-1",
      filters: { status: ["todo"] },
      refresh_requested: false,
    });
    // Filtering is not answering. If this ever fires, the board settles the
    // interaction the moment an operator touches it.
    expect(onSubmit).not.toHaveBeenCalled();
    // And the filter took effect locally.
    expect(screen.queryByTestId("app-board-card-CW-1")).toBeNull();
  });

  it("publishes a draft when a card is selected and opens the detail pane", () => {
    const onSubmit = vi.fn();
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={envelope({
          detail: {
            card_id: "CW-1",
            open: false,
            raised_by: "agent",
            sections: [{ label: "Context", markdown: "Read the *comments* first." }],
            actions: [{ id: "transition:done", label: "Mark done", tone: "primary" }],
          },
        })}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );

    expect(screen.queryByTestId("app-board-detail")).toBeNull();
    fireEvent.click(screen.getByTestId("app-board-card-CW-1"));

    expect(onDraft).toHaveBeenCalledWith(
      expect.objectContaining({ selected_card_id: "CW-1", detail_open: true }),
    );
    expect(onSubmit).not.toHaveBeenCalled();

    const detail = screen.getByTestId("app-board-detail");
    expect(detail).toHaveTextContent("Fix the transport");
    expect(detail).toHaveTextContent("Context");
    expect(screen.getByTestId("app-board-detail-origin")).toHaveTextContent("Opened by the agent");
    expect(detail.querySelector("em")).toHaveTextContent("comments");
  });

  it("renders a detail pane the agent raised without the user selecting anything", () => {
    render(
      <AppBoard
        envelope={envelope({
          detail: { card_id: "CW-2", open: true, raised_by: "agent", sections: [] },
        })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId("app-board-detail")).toHaveTextContent("Ship the board");
  });

  it("marks a refresh request in the draft rather than fetching anything", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} onDraft={onDraft} />,
    );

    fireEvent.click(screen.getByTestId("app-board-refresh"));

    expect(onDraft).toHaveBeenCalledWith(expect.objectContaining({ refresh_requested: true }));
  });

  it("submits only when the participant presses one of the caller's actions", () => {
    const onSubmit = vi.fn();
    render(
      <AppBoard
        envelope={envelope({
          detail: {
            card_id: "CW-1",
            open: true,
            actions: [{ id: "transition:done", label: "Mark done" }],
          },
        })}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />,
    );

    // The pane is already open on CW-1, so no click is needed to reach the
    // action — clicking the selected card again would collapse it.
    fireEvent.click(screen.getByTestId("app-board-action-transition:done"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response).toMatchObject({
      v: 1,
      envelopeId: "board-env-1",
      kind: "data",
      status: "submitted",
      payload: { board_id: "board-1", action_id: "transition:done", card_id: "CW-1" },
    });
  });

  it("collapses the detail pane when the already-selected card is clicked again", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={envelope({ detail: { card_id: "CW-1", open: true, sections: [] } })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    expect(screen.getByTestId("app-board-detail")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("app-board-card-CW-1"));

    expect(screen.queryByTestId("app-board-detail")).toBeNull();
    // Closing is view state too, so the caller learns about it on their next
    // look rather than the pane silently reopening from the stale draft.
    expect(onDraft).toHaveBeenCalledWith(expect.objectContaining({ detail_open: false }));
  });

  it("works with no columns, treating the board as one implicit column", () => {
    render(
      <AppBoard
        envelope={envelope({ columns: undefined })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId("app-board-column-__all")).toHaveTextContent("All3");
  });

  it("survives with no onDraft handler at all", () => {
    // Every other kind is registered without one. Filtering must not throw.
    render(<AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    expect(() => fireEvent.click(screen.getByRole("button", { name: /Doing 1/ }))).not.toThrow();
    expect(screen.queryByTestId("app-board-card-CW-2")).toBeNull();
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    render(
      <AppBoard
        envelope={{
          ...envelope(),
          context: proseProbe("ab-context"),
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseRendered("ab-context");
  });
});

// ── Staged changes and the sync button (CW-20260910-0031) ───────────────────
//
// The claim these hold: staging is not applying. A board a participant moved
// cards around in and then abandoned changes nothing anywhere, because the
// press is the decision and the draft is only where the intent accumulated
// (ADR 0007 §5).

const syncBlock = {
  enabled: true,
  endpoint: "/api/plugins/torque-board/sync",
  label: "Sync",
  stage_label: "Move to",
  scope: "3 task(s) in todo, doing.",
};

function syncEnvelope(): AppBoardEnvelope {
  return envelope({ sync: syncBlock });
}

describe("isPluginRoutePath", () => {
  it("accepts a same-origin path under the reserved plugin prefix", () => {
    expect(isPluginRoutePath("/api/plugins/torque-board/sync")).toBe(true);
  });

  it("refuses anything that could aim the board somewhere else", () => {
    // A board is not a general fetch surface; the effect broker is what
    // mediates a renderer acting on the world.
    for (const endpoint of [
      undefined,
      "",
      "https://evil.test/steal",
      "//evil.test/steal",
      "/api/hitl/items/1/resolve",
      "/api/plugins/../hitl",
      "/api/plugins/x/sync?to=elsewhere",
      "/api/plugins/x/sync#frag",
    ]) {
      expect(isPluginRoutePath(endpoint)).toBe(false);
    }
  });
});

describe("applyStagedColumns", () => {
  const columns = [
    { id: "todo", label: "Todo", card_ids: ["CW-2", "CW-3"] },
    { id: "doing", label: "Doing", card_ids: ["CW-1"] },
  ];

  it("moves a staged card into its target column and out of its old one", () => {
    const staged = applyStagedColumns(columns, { "CW-2": "doing" });
    expect(staged[0].card_ids).toEqual(["CW-3"]);
    expect(staged[1].card_ids).toEqual(["CW-1", "CW-2"]);
  });

  it("leaves a card alone when it was staged into a column the board lacks", () => {
    // Losing a card to a control that looked like it worked is worse than the
    // control doing nothing.
    const staged = applyStagedColumns(columns, { "CW-2": "archived" });
    expect(staged[0].card_ids).toEqual(["CW-2", "CW-3"]);
  });

  it("returns the columns untouched when nothing is staged", () => {
    expect(applyStagedColumns(columns, {})).toBe(columns);
  });
});

describe("<AppBoard> staging", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("offers no staging control and no sync button when the caller sends no sync block", () => {
    render(<AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.queryByTestId("app-board-sync")).toBeNull();
    expect(screen.getByTestId("app-board-refresh")).toBeInTheDocument();
  });

  it("stages a card into another column without submitting or fetching anything", () => {
    const onSubmit = vi.fn();
    const onDraft = vi.fn();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.click(screen.getByTestId("app-board-stage-doing"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_changes: { "CW-2": { column_id: "doing" } } }),
    );
    // The card renders where the participant put it, marked as not yet applied.
    expect(screen.getByTestId("app-board-column-doing")).toHaveTextContent("Ship the board");
    expect(screen.getByTestId("app-board-staged-CW-2")).toBeInTheDocument();
    expect(screen.getByTestId("app-board-staged-count")).toHaveTextContent("1 staged");
    expect(screen.getByTestId("app-board-stage-note")).toHaveTextContent("until you press Sync");
  });

  it("unstages a card moved back into the column it came from", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.click(screen.getByTestId("app-board-stage-doing"));
    fireEvent.click(screen.getByTestId("app-board-stage-todo"));

    expect(onDraft).toHaveBeenLastCalledWith(expect.objectContaining({ staged_changes: {} }));
    expect(screen.queryByTestId("app-board-staged-CW-2")).toBeNull();
  });

  it("posts the staged draft to the caller's plugin route when Sync is pressed", async () => {
    const onDraft = vi.fn();
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
        roomID="room-1"
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.click(screen.getByTestId("app-board-stage-doing"));
    fireEvent.click(screen.getByTestId("app-board-sync"));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/plugins/torque-board/sync");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ board_id: "board-1", room_id: "room-1" });
    // The draft is written before the request, because the draft is what the
    // server reads to learn what was staged.
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_changes: { "CW-2": { column_id: "doing" } } }),
    );
  });

  it("shows the server's refusal rather than pretending the sync worked", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      json: async () => ({ code: "plugin_error", message: "torque is unavailable" }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AppBoard envelope={syncEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("app-board-sync"));

    await waitFor(() =>
      expect(screen.getByTestId("app-board-sync-error")).toHaveTextContent("torque is unavailable"),
    );
  });

  it("refuses to point the board at an endpoint outside the plugin prefix", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(
      <AppBoard
        envelope={envelope({ sync: { ...syncBlock, endpoint: "https://evil.test/steal" } })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    // No button at all: an endpoint the board may not call is a board with no
    // sync, not a board that tries and fails.
    expect(screen.queryByTestId("app-board-sync")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows the caller's own scope sentence alongside the filter-bar one", () => {
    render(<AppBoard envelope={syncEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const scope = screen.getByTestId("app-board-scope");
    expect(scope).toHaveTextContent("Filters narrow what the caller sent");
    expect(scope).toHaveTextContent("3 task(s) in todo, doing.");
  });
});

// ── Staged notes (CW-20260910-0054) ─────────────────────────────────────────
//
// The additive half of the same claim: a note is view state on exactly the
// terms a staged column move is. It goes into the draft, it means nothing to
// the owning application until the participant presses Sync, and this component
// neither interprets it nor sends it anywhere itself.
//
// The kind stays domain-free. Nothing here knows the first consumer asks an
// agent to reword a Tesseract record — a note is a string on a card.

const noteSyncBlock = { ...syncBlock, note_label: "Ask the agent to reword this" };

function noteEnvelope(): AppBoardEnvelope {
  return envelope({ sync: noteSyncBlock });
}

/** A note board where one card already carries a note the caller has on record. */
function noteOnRecordEnvelope(cardID: string, note: string): AppBoardEnvelope {
  return envelope({
    sync: noteSyncBlock,
    cards: cards.map((card) => (card.id === cardID ? { ...card, note } : card)),
  });
}

describe("AppBoard staged notes", () => {
  it("offers no note box when the caller names no note_label", () => {
    render(<AppBoard envelope={syncEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    expect(screen.queryByTestId("app-board-note")).toBeNull();
  });

  it("records a note in the draft without submitting or fetching anything", () => {
    const onSubmit = vi.fn();
    const onDraft = vi.fn();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppBoard
        envelope={noteEnvelope()}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    const box = screen.getByTestId("app-board-note-CW-2");
    fireEvent.change(box, { target: { value: "say this in plainer words" } });
    // Typing alone writes no draft: a revision per keystroke would be a durable
    // write per keystroke. The box publishes when it loses focus.
    fireEvent.blur(box);

    expect(onSubmit).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_notes: { "CW-2": "say this in plainer words" } }),
    );
    expect(screen.getByTestId("app-board-noted-CW-2")).toBeInTheDocument();
    expect(screen.getByTestId("app-board-note-hint")).toHaveTextContent("until you press Sync");
  });

  it("leaves nothing pending when a box with nothing on record is emptied again", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={noteEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    const box = screen.getByTestId("app-board-note-CW-2");
    fireEvent.change(box, { target: { value: "typed something" } });
    fireEvent.blur(box);
    fireEvent.change(box, { target: { value: "" } });
    fireEvent.blur(box);

    // The key survives as an empty string rather than being deleted — the
    // caller needs to tell "cleared" from "never written" — but nothing about
    // this card is pending, so it carries no marker and no hint.
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_notes: { "CW-2": "" } }),
    );
    expect(screen.queryByTestId("app-board-noted-CW-2")).toBeNull();
    expect(screen.queryByTestId("app-board-note-hint")).toBeNull();
  });

  it("seeds the box from the note the caller has on record", () => {
    const env = noteOnRecordEnvelope("CW-2", "say this in plainer words");
    render(<AppBoard envelope={env} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));

    expect(screen.getByTestId("app-board-note-CW-2")).toHaveValue("say this in plainer words");
    // On record, so it is not pending anything — marking it would tell the
    // participant they have unsaved work forever.
    expect(screen.getByTestId("app-board-note-hint")).toHaveTextContent(
      "On record with the caller",
    );
    expect(screen.queryByTestId("app-board-noted-CW-2")).toBeNull();
    expect(screen.queryByTestId("app-board-staged-count")).toBeNull();
  });

  it("marks a note only once it differs from what is on record", () => {
    const env = noteOnRecordEnvelope("CW-2", "original");
    render(<AppBoard envelope={env} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.change(screen.getByTestId("app-board-note-CW-2"), {
      target: { value: "changed my mind" },
    });

    expect(screen.getByTestId("app-board-noted-CW-2")).toBeInTheDocument();
    expect(screen.getByTestId("app-board-note-hint")).toHaveTextContent("until you press Sync");
  });

  it("keeps an emptied box as an empty string so the caller can tell it was cleared", () => {
    const onDraft = vi.fn();
    const env = noteOnRecordEnvelope("CW-2", "original");
    render(<AppBoard envelope={env} onSubmit={vi.fn()} onCancel={vi.fn()} onDraft={onDraft} />);
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    const box = screen.getByTestId("app-board-note-CW-2");
    fireEvent.change(box, { target: { value: "" } });
    fireEvent.blur(box);

    // The key survives with an empty value. Dropping it would be
    // indistinguishable from a card nobody ever wrote on, and the caller needs
    // that difference to tell a withdrawal from silence.
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_notes: { "CW-2": "" } }),
    );
    expect(screen.getByTestId("app-board-note-hint")).toHaveTextContent("withdraw");
  });

  it("sends a note typed and never blurred, because Sync publishes the draft too", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const onDraft = vi.fn();

    render(
      <AppBoard
        envelope={noteEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
        roomID="room-1"
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.change(screen.getByTestId("app-board-note-CW-2"), {
      target: { value: "reword this" },
    });
    fireEvent.click(screen.getByTestId("app-board-sync"));

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_notes: { "CW-2": "reword this" } }),
    );
  });
});

// ── Dragging a card into a column (CW-20260910-0135) ────────────────────────
//
// Chrispian, on first use of the Tesseract review board: "Click to drag is not
// working." It never was. Kanban was chosen on the reasoning that MOVING a card
// between columns IS the disposition, and clicking a button in a side pane is a
// weaker version of that gesture — weakest exactly where the surface is aimed,
// a triage pass over many records.
//
// What these hold: a drop is a second GESTURE onto the same view state, never a
// second meaning. It writes the same draft the button writes, it settles
// nothing, and it fetches nothing.

/** A DataTransfer stub good enough for the three calls the component makes. */
function dataTransfer(initial: Record<string, string> = {}) {
  const store: Record<string, string> = { ...initial };
  return {
    types: Object.keys(store),
    dropEffect: "",
    effectAllowed: "",
    setData: (type: string, value: string) => {
      store[type] = value;
    },
    getData: (type: string) => store[type] ?? "",
  };
}

describe("AppBoard drag staging", () => {
  it("stages a card dropped into another column, exactly as the button does", () => {
    const onSubmit = vi.fn();
    const onDraft = vi.fn();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );

    const transfer = dataTransfer();
    fireEvent.dragStart(screen.getByTestId("app-board-card-CW-2"), {
      dataTransfer: transfer,
    });
    expect(transfer.getData(CARD_DRAG_TYPE)).toBe("CW-2");

    const target = screen.getByTestId("app-board-column-doing");
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });

    // Same draft the detail-pane button writes, and nothing else happened.
    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_changes: { "CW-2": { column_id: "doing" } } }),
    );
    expect(onSubmit).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByTestId("app-board-column-doing")).toHaveTextContent("Ship the board");
    expect(screen.getByTestId("app-board-staged-CW-2")).toBeInTheDocument();
  });

  it("unstages a card dropped back into the column it came from", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );

    const transfer = dataTransfer();
    const move = (columnID: string) => {
      fireEvent.dragStart(screen.getByTestId("app-board-card-CW-2"), { dataTransfer: transfer });
      const target = screen.getByTestId(`app-board-column-${columnID}`);
      fireEvent.dragOver(target, { dataTransfer: transfer });
      fireEvent.drop(target, { dataTransfer: transfer });
    };

    move("doing");
    move("todo");

    // Undoing a move leaves nothing behind for the sync to apply — the same
    // rule the button path follows.
    expect(onDraft).toHaveBeenLastCalledWith(expect.objectContaining({ staged_changes: {} }));
    expect(screen.queryByTestId("app-board-staged-CW-2")).toBeNull();
  });

  it("ignores a drop that does not carry one of this board's cards", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );

    const target = screen.getByTestId("app-board-column-doing");
    // A card id from some other board — a stale drag that outlived a sync.
    const foreign = dataTransfer({ [CARD_DRAG_TYPE]: "CW-999" });
    fireEvent.dragOver(target, { dataTransfer: foreign });
    fireEvent.drop(target, { dataTransfer: foreign });

    expect(onDraft).not.toHaveBeenCalled();
  });

  it("offers no dragging when the caller offers no staging", () => {
    render(<AppBoard envelope={envelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    // A read-only board is not a drop target, and its cards do not pick up.
    expect(screen.getByTestId("app-board-card-CW-2")).not.toHaveAttribute("draggable", "true");
  });

  it("keeps the button path working, because dragging is not keyboard reachable", () => {
    const onDraft = vi.fn();
    render(
      <AppBoard
        envelope={syncEnvelope()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        onDraft={onDraft}
      />,
    );
    fireEvent.click(screen.getByTestId("app-board-card-CW-2"));
    fireEvent.click(screen.getByTestId("app-board-stage-doing"));

    expect(onDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ staged_changes: { "CW-2": { column_id: "doing" } } }),
    );
  });
});
