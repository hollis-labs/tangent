import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AppBoard, type AppBoardEnvelope, applyFilters, type BoardCardData } from "./AppBoard";

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
});
