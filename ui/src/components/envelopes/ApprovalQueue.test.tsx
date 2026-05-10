import { fireEvent, render, screen } from "@testing-library/react";
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

  it("submits normalized decisions with comments and export refs", () => {
    const onSubmit = vi.fn<(response: ApprovalQueueResponse) => void>();
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
});
