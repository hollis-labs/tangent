import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  getSpreadsheetReviewDraftStorageKey,
  SPREADSHEET_REVIEW_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/spreadsheet-review-draft-storage";
import {
  SpreadsheetReview,
  type SpreadsheetReviewEnvelope,
  type SpreadsheetReviewResponse,
} from "./SpreadsheetReview";

const baseEnvelope: SpreadsheetReviewEnvelope = {
  v: 1,
  id: "spreadsheet-1",
  type: "tangent.spreadsheet-review",
  title: "Spreadsheet review",
  data: {
    table_id: "table-1",
    intent: "Review the table",
    columns: [
      { id: "name", label: "Name" },
      { id: "status", label: "Status" },
    ],
    rows: [
      { id: "row-1", name: "Alpha", status: "open" },
      { id: "row-2", name: "Beta", status: "closed" },
    ],
    query_state: {
      search: "",
      visible_columns: ["name", "status"],
    },
    notes: "seed notes",
    row_actions: [{ id: "approve", label: "Approve" }],
  },
};

const emptyStateData: NonNullable<SpreadsheetReviewEnvelope["data"]> = {
  table_id: "table-1",
  intent: "Review the table",
  columns: [
    { id: "name", label: "Name" },
    { id: "status", label: "Status" },
  ],
  rows: [],
  query_state: {
    search: "",
    visible_columns: ["name", "status"],
  },
  notes: "",
  row_actions: [{ id: "approve", label: "Approve" }],
};

const nonSortableEnvelopeData: NonNullable<SpreadsheetReviewEnvelope["data"]> = {
  table_id: "table-1",
  intent: "Review the table",
  columns: [
    { id: "name", label: "Name", sortable: false },
    { id: "status", label: "Status" },
  ],
  rows: [
    { id: "row-1", name: "Alpha", status: "open" },
    { id: "row-2", name: "Beta", status: "closed" },
  ],
  query_state: {
    search: "",
    visible_columns: ["name", "status"],
  },
  notes: "seed notes",
  row_actions: [{ id: "approve", label: "Approve" }],
};

describe("SpreadsheetReview", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("loads a seeded table", () => {
    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByDisplayValue("seed notes")).toBeInTheDocument();
  });

  it("renders the empty state for a blank table", () => {
    render(
      <SpreadsheetReview
        envelope={{
          ...baseEnvelope,
          data: emptyStateData,
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByTestId("spreadsheet-review-empty")).toHaveTextContent(
      "No rows match the current query.",
    );
  });

  it("submits selected rows with query state and action id", () => {
    const onSubmit = vi.fn<(response: SpreadsheetReviewResponse) => void>();
    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("spreadsheet-review-sort-status"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Alpha" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-select-row-1"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-action"), {
      target: { value: "approve" },
    });
    fireEvent.change(screen.getByTestId("spreadsheet-review-notes"), {
      target: { value: "only Alpha" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      envelopeId: "spreadsheet-1",
      kind: "data",
      status: "submitted",
      payload: {
        table_id: "table-1",
        selected_row_ids: ["row-1"],
        selected_rows: [{ id: "row-1", name: "Alpha", status: "open" }],
        action_id: "approve",
        notes: "only Alpha",
        query_state: {
          search: "Alpha",
          sort: [{ column_id: "status", direction: "asc" }],
        },
      },
    });
  });

  it("submits selected row summaries from canonical rows, not only the filtered subset", () => {
    const onSubmit = vi.fn<(response: SpreadsheetReviewResponse) => void>();
    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("spreadsheet-review-select-row-1"));
    fireEvent.click(screen.getByTestId("spreadsheet-review-select-row-2"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Alpha" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload.selected_row_ids).toEqual(["row-1", "row-2"]);
    expect(onSubmit.mock.calls[0][0].payload.selected_rows).toEqual([
      { id: "row-1", name: "Alpha", status: "open" },
      { id: "row-2", name: "Beta", status: "closed" },
    ]);
  });

  it("does not render sort controls for non-sortable columns", () => {
    render(
      <SpreadsheetReview
        envelope={{
          ...baseEnvelope,
          data: nonSortableEnvelopeData,
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.queryByTestId("spreadsheet-review-sort-name")).not.toBeInTheDocument();
    expect(screen.getByTestId("spreadsheet-review-sort-status")).toBeInTheDocument();
  });

  it("saves and restores named views", () => {
    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Beta" },
    });
    fireEvent.change(screen.getByTestId("spreadsheet-review-view-name"), {
      target: { value: "Closed rows" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-save-view"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Alpha" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-restore-Closed rows"));

    expect(screen.getByTestId("spreadsheet-review-search")).toHaveValue("Beta");
  });

  it("recovers unsent draft state across refresh", () => {
    vi.useFakeTimers();
    const { unmount } = render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Beta" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-select-row-2"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-notes"), {
      target: { value: "draft note" },
    });
    vi.advanceTimersByTime(SPREADSHEET_REVIEW_AUTOSAVE_DEBOUNCE_MS + 50);
    expect(
      window.localStorage.getItem(getSpreadsheetReviewDraftStorageKey("room-a", "table-1")),
    ).toBeTruthy();

    unmount();

    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByTestId("spreadsheet-review-message")).toHaveTextContent("Recovered");
    expect(screen.getByTestId("spreadsheet-review-search")).toHaveValue("Beta");
    expect(screen.getByTestId("spreadsheet-review-notes")).toHaveValue("draft note");
  });

  it("exports the current filtered view as CSV metadata", () => {
    const createObjectURL = vi.fn(() => "blob:csv-export");
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

    render(
      <SpreadsheetReview
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.change(screen.getByTestId("spreadsheet-review-search"), {
      target: { value: "Alpha" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-export"));

    expect(createObjectURL).toHaveBeenCalled();
    expect(screen.getByTestId("spreadsheet-review-message")).toHaveTextContent("Exported 1 row");
  });
});
