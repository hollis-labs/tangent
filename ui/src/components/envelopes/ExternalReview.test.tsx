import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ExternalReview } from "./ExternalReview";

const envelope = {
  id: "review-1",
  type: "tangent.external-review",
  data: {
    review_id: "request-1",
    title: "Review PR",
    content_markdown: "Original PR body",
    summary: "Agent summary",
    resource: { url: "https://github.com/example/repo/pull/1", revision: "abc", label: "GitHub" },
    action_url: "/api/plugins/github-pr/action",
    state_url: "/api/plugins/github-pr/state",
    actions: [
      { id: "approve", label: "Approve" },
      { id: "merge", label: "Merge" },
    ],
  },
};
const reply = (value: unknown, ok = true) => ({ ok, json: async () => value });
afterEach(() => vi.unstubAllGlobals());
describe("external review", () => {
  it("keeps history read-only without contacting the plugin", () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    render(
      <ExternalReview
        envelope={envelope}
        roomID="room-1"
        readOnly
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByText("Original PR body")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Merge" })).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });
  it("approval keeps the review open and merge records its original identity", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(reply({ status: "open", message: "Ready" }))
      .mockResolvedValueOnce(
        reply({ status: "approved", message: "Approved", result: { outcome: "approved" } }),
      )
      .mockResolvedValueOnce(
        reply({
          status: "merged",
          message: "Merged",
          complete: true,
          result: { outcome: "merged", review_id: 123 },
        }),
      );
    vi.stubGlobal("fetch", fetch);
    const submit = vi.fn();
    render(
      <ExternalReview envelope={envelope} roomID="room-1" onSubmit={submit} onCancel={vi.fn()} />,
    );
    await screen.findByText("Ready");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      room_id: "room-1",
      envelope_id: "review-1",
    });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await screen.findByText("Approved");
    expect(submit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    await waitFor(() =>
      expect(submit).toHaveBeenCalledWith(
        expect.objectContaining({
          payload: expect.objectContaining({
            outcome: "merged",
            review_id: "request-1",
            resource_revision: "abc",
          }),
        }),
      ),
    );
  });
  it("shows remote refusal without completing the request", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(reply({ status: "open", message: "Ready" }))
        .mockResolvedValueOnce(
          reply({ message: "The commit changed. Request a new review." }, false),
        ),
    );
    const submit = vi.fn();
    render(
      <ExternalReview envelope={envelope} roomID="room-1" onSubmit={submit} onCancel={vi.fn()} />,
    );
    await screen.findByText("Ready");
    fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The commit changed");
    expect(submit).not.toHaveBeenCalled();
  });
});
