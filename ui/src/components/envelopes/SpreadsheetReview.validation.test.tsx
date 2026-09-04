// Validation-affordance coverage for the spreadsheet review.
//
// This workflow's terminal CTA is deliberately ungated — submitting an empty
// selection with no action is a legitimate answer, and these tests pin that so
// nobody "fixes" it into a gate later. The failures it actually had were one
// level down: Add filter and Save view both returned silently when their own
// input was empty, so the operator clicked and the UI did nothing at all.
//
// Each test names the pattern it pins so a regression reads as the pattern
// coming back rather than as an assertion count changing.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  SpreadsheetReview,
  type SpreadsheetReviewEnvelope,
  type SpreadsheetReviewResponse,
} from "./SpreadsheetReview";

const envelope: SpreadsheetReviewEnvelope = {
  v: 1,
  id: "spreadsheet-1",
  type: "tangent.spreadsheet-review",
  title: "Spreadsheet review",
  data: {
    table_id: "table-1",
    columns: [
      { id: "name", label: "Name" },
      { id: "status", label: "Status" },
    ],
    rows: [
      { id: "row-1", name: "Alpha", status: "open" },
      { id: "row-2", name: "Beta", status: "closed" },
    ],
    query_state: { search: "", visible_columns: ["name", "status"] },
    notes: "",
    row_actions: [{ id: "archive", label: "Archive rows" }],
  },
};

function renderReview(onSubmit = vi.fn<(response: SpreadsheetReviewResponse) => void>()) {
  render(
    <SpreadsheetReview
      envelope={envelope}
      onSubmit={onSubmit}
      onCancel={vi.fn()}
      roomID="room-a"
    />,
  );
  return onSubmit;
}

describe("SpreadsheetReview validation affordances", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("explains Add filter instead of failing silently on an empty value", () => {
    renderReview();

    const value = screen.getByTestId("spreadsheet-review-filter-value");
    expect(
      screen.queryByTestId("spreadsheet-review-filter-value-required"),
    ).not.toBeInTheDocument();
    expect(value).toHaveAttribute("aria-required", "false");

    // Before: this click did nothing whatsoever.
    fireEvent.click(screen.getByTestId("spreadsheet-review-add-filter"));

    const error = screen.getByTestId("spreadsheet-review-filter-value-error");
    expect(error).toHaveTextContent("Enter a value before adding this filter.");
    expect(error).toHaveAttribute("role", "alert");
    expect(screen.getByTestId("spreadsheet-review-filter-value-required")).toBeInTheDocument();
    expect(value).toHaveAttribute("aria-required", "true");
    expect(value).toHaveAttribute("aria-invalid", "true");
    expect(value).toHaveAttribute(
      "aria-describedby",
      "spreadsheet-review-filter-value-hint spreadsheet-review-filter-value-error",
    );
    // The error sits beside the control that clears it, not in a banner above.
    expect(document.querySelector("label[for='spreadsheet-review-filter-value']")).toBeTruthy();

    fireEvent.change(value, { target: { value: "open" } });
    expect(screen.queryByTestId("spreadsheet-review-filter-value-error")).not.toBeInTheDocument();
    expect(value).toHaveAttribute("aria-describedby", "spreadsheet-review-filter-value-hint");

    fireEvent.click(screen.getByTestId("spreadsheet-review-add-filter"));
    expect(screen.getByTestId("spreadsheet-review-active-filters")).toHaveTextContent(
      "name contains open",
    );
  });

  it("explains Add filter when the table has no column to filter on", () => {
    render(
      <SpreadsheetReview
        envelope={{ ...envelope, data: { ...envelope.data, table_id: "table-1", columns: [] } }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-b"
      />,
    );

    fireEvent.click(screen.getByTestId("spreadsheet-review-add-filter"));

    const error = screen.getByTestId("spreadsheet-review-filter-column-error");
    expect(error).toHaveTextContent("This table has no column to filter on.");
    expect(error).toHaveAttribute("role", "alert");
    expect(screen.getByTestId("spreadsheet-review-filter-column")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("explains Save view instead of failing silently on a blank name", () => {
    renderReview();

    const name = screen.getByTestId("spreadsheet-review-view-name");
    expect(screen.queryByTestId("spreadsheet-review-view-name-required")).not.toBeInTheDocument();

    // Whitespace was accepted by the input and rejected by the handler, in
    // silence. Now it says so.
    fireEvent.change(name, { target: { value: "   " } });
    fireEvent.click(screen.getByTestId("spreadsheet-review-save-view"));

    const error = screen.getByTestId("spreadsheet-review-view-name-error");
    expect(error).toHaveTextContent("Name this view before saving it.");
    expect(error).toHaveAttribute("role", "alert");
    expect(screen.getByTestId("spreadsheet-review-view-name-required")).toBeInTheDocument();
    expect(name).toHaveAttribute("aria-required", "true");
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAttribute(
      "aria-describedby",
      "spreadsheet-review-view-name-hint spreadsheet-review-view-name-error",
    );
    expect(screen.getByText("No saved views yet.")).toBeInTheDocument();

    fireEvent.change(name, { target: { value: "Open rows" } });
    fireEvent.click(screen.getByTestId("spreadsheet-review-save-view"));
    expect(screen.queryByTestId("spreadsheet-review-view-name-error")).not.toBeInTheDocument();
    expect(screen.getByTestId("spreadsheet-review-restore-Open rows")).toBeInTheDocument();
  });

  it("keeps Submit ungated: an empty selection with no action is still a valid answer", () => {
    const onSubmit = renderReview();

    const submit = screen.getByTestId("spreadsheet-review-submit");
    expect(submit).not.toBeDisabled();
    expect(submit).not.toHaveAttribute("aria-describedby");
    // No terminal submit gate exists here on purpose — this workflow never had
    // one, and surfacing a gate that does not exist would invent validation.
    expect(screen.queryByTestId("spreadsheet-review-submit-gate")).not.toBeInTheDocument();

    fireEvent.click(submit);

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload.selected_row_ids).toEqual([]);
    expect(onSubmit.mock.calls[0][0].payload.action_id).toBeUndefined();
  });

  it("names every unlabelled control and every row checkbox", () => {
    renderReview();

    const pairs: Array<[string, string]> = [
      ["spreadsheet-review-search", "Search rows"],
      ["spreadsheet-review-filter-column", "Filter column"],
      ["spreadsheet-review-filter-op", "Match"],
      ["spreadsheet-review-filter-value", "Filter value"],
      ["spreadsheet-review-view-name", "View name"],
      ["spreadsheet-review-action", "Bulk action"],
      ["spreadsheet-review-notes", "Notes (optional)"],
    ];
    for (const [id, text] of pairs) {
      expect(screen.getByTestId(id)).toHaveAttribute("id", id);
      expect(document.querySelector(`label[for='${id}']`)).toHaveTextContent(text);
    }

    // The `<th>Select</th>` never named these; each now announces its own row.
    expect(screen.getByRole("checkbox", { name: "Select row Alpha" })).toBe(
      screen.getByTestId("spreadsheet-review-select-row-1"),
    );
    expect(screen.getByRole("checkbox", { name: "Select row Beta" })).toBe(
      screen.getByTestId("spreadsheet-review-select-row-2"),
    );
  });

  it("separates the optional note from the bulk action beside it", () => {
    renderReview();

    const notes = screen.getByTestId("spreadsheet-review-notes");
    expect(document.querySelector("label[for='spreadsheet-review-notes']")).toHaveTextContent(
      "Notes (optional)",
    );
    expect(notes).toHaveAttribute("placeholder", "Freeform note submitted with this review");
    expect(notes).toHaveAttribute("aria-describedby", "spreadsheet-review-notes-hint");
    expect(document.getElementById("spreadsheet-review-notes-hint")).toHaveTextContent(
      "It is not a reason for the bulk action, and the bulk action does not require one.",
    );
    // Optional means optional: nothing marks it required, in either direction.
    expect(notes).not.toHaveAttribute("aria-required", "true");
    expect(document.getElementById("spreadsheet-review-action-hint")).toHaveTextContent(
      "Optional.",
    );
  });

  it("announces the export and view-restore status politely", () => {
    renderReview();

    fireEvent.change(screen.getByTestId("spreadsheet-review-view-name"), {
      target: { value: "Everything" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-save-view"));

    const message = screen.getByTestId("spreadsheet-review-message");
    expect(message).toHaveAttribute("role", "status");
    expect(message).toHaveAttribute("aria-live", "polite");
    expect(message).toHaveTextContent('Saved view "Everything".');
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderReview();

    fireEvent.click(screen.getByTestId("spreadsheet-review-select-row-1"));
    fireEvent.change(screen.getByTestId("spreadsheet-review-action"), {
      target: { value: "archive" },
    });
    fireEvent.change(screen.getByTestId("spreadsheet-review-notes"), {
      target: { value: "Archiving the stale row." },
    });
    fireEvent.change(screen.getByTestId("spreadsheet-review-filter-value"), {
      target: { value: "open" },
    });
    fireEvent.click(screen.getByTestId("spreadsheet-review-add-filter"));
    fireEvent.click(screen.getByTestId("spreadsheet-review-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload.table_id).toBe("table-1");
    expect(response.payload.selected_row_ids).toEqual(["row-1"]);
    expect(response.payload.selected_rows).toEqual([
      { id: "row-1", name: "Alpha", status: "open" },
    ]);
    expect(response.payload.action_id).toBe("archive");
    expect(response.payload.notes).toBe("Archiving the stale row.");
    expect(response.payload.query_state).toMatchObject({
      filters: [{ column_id: "name", op: "contains", value: "open" }],
      visible_columns: ["name", "status"],
    });
    expect(response.payload.saved_views).toEqual([]);
    expect(response.payload.export_refs).toEqual([]);
  });
});
