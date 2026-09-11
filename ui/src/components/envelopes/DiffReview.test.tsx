import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { expectProseLiteral, proseProbe } from "@/components/markdown/prose-probe";
import {
  DIFF_REVIEW_AUTOSAVE_DEBOUNCE_MS,
  getDiffReviewDraftStorageKey,
} from "@/lib/diff-review-draft-storage";
import { DiffReview, type DiffReviewEnvelope, type DiffReviewResponse } from "./DiffReview";

const baseEnvelope: DiffReviewEnvelope = {
  v: 1,
  id: "diff-1",
  type: "tangent.diff-review",
  title: "Diff review",
  data: {
    review_id: "review-1",
    before_ref: { artifact_id: "artifact-before", name: "before.patch" },
    after_ref: { artifact_id: "artifact-after", name: "after.patch" },
    files: [
      {
        id: "file-1",
        path: "pkg/app.go",
        summary: "Tighten validation",
        hunks: [
          {
            id: "hunk-1",
            header: "@@ -1,3 +1,4 @@",
            before: "return nil",
            after: "return validate()",
            action_options: [{ id: "follow-up", label: "Follow-up" }],
          },
        ],
      },
      {
        id: "file-2",
        path: "ui/view.tsx",
        summary: "Polish spacing",
        hunks: [
          {
            id: "hunk-2",
            header: "@@ -2,3 +2,4 @@",
            before: "gap-2",
            after: "gap-3",
          },
        ],
      },
    ],
  },
};

describe("DiffReview", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("submits normalized decisions and export refs", () => {
    const onSubmit = vi.fn<(response: DiffReviewResponse) => void>();
    const click = vi.fn();
    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "a") {
        return { click, href: "", download: "" } as unknown as HTMLAnchorElement;
      }
      return originalCreateElement(tagName);
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:diff-review");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});

    render(
      <DiffReview envelope={baseEnvelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.click(screen.getByTestId("diff-review-hunk-decision-hunk-1-accept"));
    fireEvent.change(screen.getByDisplayValue("No action ID"), {
      target: { value: "follow-up" },
    });
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-1-notes"), {
      target: { value: "Looks good." },
    });
    fireEvent.click(screen.getByTestId("diff-review-file-file-2"));
    fireEvent.click(screen.getByTestId("diff-review-batch-reject"));
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-2-notes"), {
      target: { value: "Spacing regressed." },
    });
    fireEvent.click(screen.getByTestId("diff-review-export"));
    fireEvent.click(screen.getByTestId("diff-review-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      envelopeId: "diff-1",
      kind: "data",
      status: "submitted",
      payload: {
        review_id: "review-1",
        decisions: [
          {
            file_id: "file-1",
            hunk_id: "hunk-1",
            decision: "accept",
            action_id: "follow-up",
          },
          {
            file_id: "file-2",
            hunk_id: "hunk-2",
            decision: "reject",
          },
        ],
        comments: {
          "file-1::hunk-1": "Looks good.",
          "file-2::hunk-2": "Spacing regressed.",
        },
      },
    });
    expect(onSubmit.mock.calls[0][0].payload.export_refs).toHaveLength(1);
  });

  it("recovers unsent draft state across refresh", () => {
    vi.useFakeTimers();
    const { unmount } = render(
      <DiffReview envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    fireEvent.click(screen.getByTestId("diff-review-file-file-2"));
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-2-notes"), {
      target: { value: "Need stronger rationale." },
    });
    vi.advanceTimersByTime(DIFF_REVIEW_AUTOSAVE_DEBOUNCE_MS + 50);

    expect(
      window.localStorage.getItem(getDiffReviewDraftStorageKey("room-a", "review-1")),
    ).toBeTruthy();
    unmount();

    render(
      <DiffReview envelope={baseEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    expect(screen.getByTestId("diff-review-message")).toHaveTextContent("Recovered");
    expect(screen.getByTestId("diff-review-file-file-2")).toBeInTheDocument();
    expect(screen.getByTestId("diff-review-hunk-decision-hunk-2-notes")).toHaveValue(
      "Need stronger rationale.",
    );
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    const envelope: DiffReviewEnvelope = {
      v: 1,
      id: "diff-md",
      type: "tangent.diff-review",
      data: {
        review_id: "diff-md",
        files: [
          {
            id: "f1",
            path: "a.ts",
            summary: proseProbe("dr-summary"),
            before: "old",
            after: "new",
          },
        ],
      },
    };

    render(<DiffReview envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    // The sidebar row and the file header render the same string; both go
    // through the renderer, so the probe appears twice.
    expect(screen.getAllByText("dr-summary-bold", { selector: "strong" })).toHaveLength(2);
    expect(screen.getAllByText("dr-summary-one", { selector: "li" })).toHaveLength(2);
  });

  // The Leave bucket, pinned: a diff hunk is evidence the reader compares
  // character by character, so it stays literal no matter what it contains.
  it("keeps diff hunks literal", () => {
    const envelope: DiffReviewEnvelope = {
      v: 1,
      id: "diff-literal",
      type: "tangent.diff-review",
      data: {
        review_id: "diff-literal",
        files: [
          {
            id: "f1",
            path: "a.ts",
            before: proseProbe("dr-before"),
            after: proseProbe("dr-after"),
          },
        ],
      },
    };

    render(<DiffReview envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expectProseLiteral(screen.getByTestId("diff-review-pane-before"), "dr-before");
    expectProseLiteral(screen.getByTestId("diff-review-pane-after"), "dr-after");
  });
});
