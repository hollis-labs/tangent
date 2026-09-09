import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  APPROVAL_QUEUE_AUTOSAVE_DEBOUNCE_MS,
  getApprovalQueueDraftStorageKey,
} from "@/lib/approval-queue-draft-storage";
import {
  ApprovalQueue,
  type ApprovalQueueEnvelope,
  type ApprovalQueueResponse,
} from "./ApprovalQueue";

const baseEnvelope: ApprovalQueueEnvelope = {
  v: 1,
  id: "approval-1",
  type: "tangent.approval-queue",
  title: "Approval queue",
  data: {
    queue_id: "queue-1",
    title: "Approval queue",
    intent: "Review each change.",
    current_index: 0,
    items: [
      {
        id: "item-1",
        title: "Update dependency",
        summary: "Low-risk patch release",
        description: "Bump a direct dependency from 1.0.0 to 1.0.1.",
        evidence: [{ id: "ev-1", label: "Diff", kind: "diff", content: "package.json delta" }],
        action_options: [{ id: "merge", label: "Merge" }],
      },
      {
        id: "item-2",
        title: "Enable feature flag",
        summary: "Needs follow-up",
        description: "Flip rollout to 100%.",
      },
    ],
    notes: "seed notes",
  },
};

describe("ApprovalQueue", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  // The reported defect: an agent writes a proposal in markdown and the
  // operator reviews it as one unbroken run of plain text.
  it("renders the item body as markdown and leaves literal evidence unparsed", () => {
    render(
      <ApprovalQueue
        envelope={{
          ...baseEnvelope,
          data: {
            ...baseEnvelope.data,
            queue_id: "queue-markdown",
            items: [
              {
                id: "item-md",
                title: "Gate r1",
                summary: "Three gates",
                body: "## Gates\n\n- **first** gate\n- second gate",
                evidence: [
                  {
                    id: "ev-note",
                    label: "Rationale",
                    kind: "note",
                    content: "## Why\n\nBecause.",
                  },
                  {
                    id: "ev-diff",
                    label: "Diff",
                    kind: "diff",
                    content: "- old\n+ # not a heading",
                  },
                ],
              },
            ],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const description = screen.getByTestId("approval-queue-description");
    expect(within(description).getByRole("heading", { name: "Gates" })).toBeInTheDocument();
    expect(within(description).getAllByRole("listitem")).toHaveLength(2);
    expect(within(description).getByText("first").tagName).toBe("STRONG");

    // An unnamed kind normalizes to `note`, which is prose and gets parsed.
    const evidence = screen.getByTestId("approval-queue-evidence-content");
    expect(within(evidence).getByRole("heading", { name: "Why" })).toBeInTheDocument();

    // A diff is not prose: the `#` on the added line is data, not a heading.
    fireEvent.click(screen.getByTestId("approval-queue-evidence-tab-1"));
    const diffPane = screen.getByTestId("approval-queue-evidence-content");
    expect(within(diffPane).queryByRole("heading")).not.toBeInTheDocument();
    expect(within(diffPane).getByText(/# not a heading/)).toBeInTheDocument();
  });

  it("submits normalized decisions with comments and export refs", () => {
    const onSubmit = vi.fn<(response: ApprovalQueueResponse) => void>();
    const click = vi.fn();
    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "a") {
        return { click, href: "", download: "" } as unknown as HTMLAnchorElement;
      }
      return originalCreateElement(tagName);
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:approval-queue");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    render(
      <ApprovalQueue
        envelope={baseEnvelope}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("approval-queue-decision-accept"));
    fireEvent.change(screen.getByTestId("approval-queue-action"), { target: { value: "merge" } });
    fireEvent.change(screen.getByTestId("approval-queue-comment"), {
      target: { value: "Safe change" },
    });
    fireEvent.click(screen.getByTestId("approval-queue-next"));
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));
    fireEvent.change(screen.getByTestId("approval-queue-defer-reason"), {
      target: { value: "Need rollout window" },
    });
    fireEvent.click(screen.getByTestId("approval-queue-export"));
    fireEvent.click(screen.getByTestId("approval-queue-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      envelopeId: "approval-1",
      kind: "data",
      status: "submitted",
      payload: {
        queue_id: "queue-1",
        current_index: 1,
        notes: "seed notes",
        decisions: [
          {
            item_id: "item-1",
            decision: "accept",
            action_id: "merge",
            comment: "Safe change",
          },
          {
            item_id: "item-2",
            decision: "defer",
            defer_reason: "Need rollout window",
          },
        ],
      },
    });
    expect(onSubmit.mock.calls[0][0].payload.export_refs).toHaveLength(1);
    expect(onSubmit.mock.calls[0][0].payload.export_refs[0]?.name).toMatch(
      /^queue-1-audit-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}/,
    );
  });

  it("recovers unsent draft state across refresh", () => {
    vi.useFakeTimers();
    const { unmount } = render(
      <ApprovalQueue
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    fireEvent.click(screen.getByTestId("approval-queue-next"));
    fireEvent.click(screen.getByTestId("approval-queue-decision-reject"));
    fireEvent.change(screen.getByTestId("approval-queue-comment"), {
      target: { value: "Not enough evidence" },
    });
    vi.advanceTimersByTime(APPROVAL_QUEUE_AUTOSAVE_DEBOUNCE_MS + 50);

    expect(
      window.localStorage.getItem(getApprovalQueueDraftStorageKey("room-a", "queue-1")),
    ).toBeTruthy();
    unmount();

    render(
      <ApprovalQueue
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(screen.getByTestId("approval-queue-message")).toHaveTextContent("Recovered");
    expect(screen.getByTestId("approval-queue-item-item-2")).toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-comment")).toHaveValue("Not enough evidence");
  });

  it("drops corrupted draft indexes and disables submit when no items exist", () => {
    window.localStorage.setItem(
      getApprovalQueueDraftStorageKey("room-a", "queue-empty"),
      JSON.stringify({
        version: 1,
        roomID: "room-a",
        queueID: "queue-empty",
        envelopeId: "approval-empty",
        baseSeedKey: "{}",
        currentIndex: Number.NaN,
        decisions: [],
        notes: "",
        exportRefs: [],
        savedAt: new Date().toISOString(),
      }),
    );

    render(
      <ApprovalQueue
        envelope={{
          ...baseEnvelope,
          id: "approval-empty",
          data: {
            queue_id: "queue-empty",
            title: "Empty queue",
            current_index: 0,
            items: [],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(
      window.localStorage.getItem(getApprovalQueueDraftStorageKey("room-a", "queue-empty")),
    ).toBeNull();
    expect(screen.getByTestId("approval-queue-submit")).toBeDisabled();
    expect(screen.getByText("No queue items were provided.")).toBeInTheDocument();
  });

  it.each([
    ["unknown decision", { decision: "approve" }],
    ["non-string comment", { comment: { text: "unsafe" } }],
    ["non-string action", { action_id: 42 }],
    ["non-string defer reason", { decision: "defer", defer_reason: null }],
  ])("drops a corrupted recovered draft with %s", (_label, corruptFields) => {
    const storageKey = getApprovalQueueDraftStorageKey("room-a", "queue-1");
    window.localStorage.setItem(
      storageKey,
      JSON.stringify({
        version: 1,
        roomID: "room-a",
        queueID: "queue-1",
        envelopeId: "approval-1",
        baseSeedKey: "{}",
        currentIndex: 1,
        decisions: [
          {
            item_id: "item-1",
            decision: "accept",
            comment: "",
            action_id: "merge",
            defer_reason: "",
          },
          {
            item_id: "item-2",
            decision: "reject",
            comment: "",
            action_id: "",
            defer_reason: "",
            ...corruptFields,
          },
        ],
        notes: "unsafe recovered notes",
        exportRefs: [],
        savedAt: new Date().toISOString(),
      }),
    );

    render(
      <ApprovalQueue
        envelope={baseEnvelope}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-a"
      />,
    );

    expect(window.localStorage.getItem(storageKey)).toBeNull();
    expect(screen.getByTestId("approval-queue-submit")).toBeDisabled();
    expect(screen.queryByText(/Recovered unsent approval-queue state/)).not.toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-notes")).toHaveValue("seed notes");
  });
});
