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
const refusedPaths = [
  "/api/plugins/x/a/../../../inbox/items/abc/archive",
  "/api/plugins/x/%2e%2e/%2e%2e/inbox/items/abc/archive",
  "/api/plugins/x/a%2f..%2f..%2finbox",
  "/api/plugins/x/./a",
  "/api/plugins/x//a",
  "/api/plugins/x/a?to=/api/inbox",
  "/api/plugins/x/a#frag",
  "/api/hitl/items/1/resolve",
  "//evil.test/api/plugins/x/a",
  "https://evil.test/api/plugins/x/a",
];
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
  it("makes no request and offers no action when either URL is a refused path", async () => {
    for (const bad of refusedPaths) {
      for (const field of ["state_url", "action_url"] as const) {
        const fetch = vi.fn();
        vi.stubGlobal("fetch", fetch);
        const { unmount } = render(
          <ExternalReview
            envelope={{ ...envelope, data: { ...envelope.data, [field]: bad } }}
            roomID="room-1"
            onSubmit={vi.fn()}
            onCancel={vi.fn()}
          />,
        );
        expect(screen.getByRole("alert")).toHaveTextContent(
          "The review plugin route is unavailable.",
        );
        for (const name of ["Approve", "Merge"]) {
          const button = screen.getByRole("button", { name }) as HTMLButtonElement;
          expect(button.disabled, `${field} ${bad}`).toBe(true);
          fireEvent.click(button);
        }
        await Promise.resolve();
        expect(fetch, `${field} ${bad}`).not.toHaveBeenCalled();
        unmount();
      }
    }
  });
});
