import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxEntry } from "@/lib/inbox-api";
import Inbox from "./Inbox";

const { fetchInbox } = vi.hoisted(() => ({ fetchInbox: vi.fn() }));
vi.mock("@/lib/inbox-api", async (original) => ({
  ...(await original<typeof import("@/lib/inbox-api")>()),
  fetchInbox,
}));
vi.mock("./InboxItemBody", () => ({
  InboxItemBody: ({ entry }: { entry: InboxEntry }) => (
    <p>Body for {entry.interaction.interaction_id}</p>
  ),
}));
vi.mock("./Room", () => ({
  default: ({ roomID }: { roomID: string }) => <p>Interactive workflow in {roomID}</p>,
}));
vi.mock("@/components/envelopes/EnvelopeRouter", () => ({
  EnvelopeRouter: () => <p>Original workflow request</p>,
}));

function entry(id: string, sequence: number, kind: string): InboxEntry {
  return {
    sequence,
    interaction: {
      interaction_id: id,
      definition_binding: { kind },
      request_snapshot: { title: `Request ${id}`, source: { agent_label: "Test agent" } },
      caller_scope: "standalone-local:test",
      state: "presented",
      revision: 1,
      created_at: "2026-10-01T10:00:00Z",
      updated_at: "2026-10-01T10:00:00Z",
      ...(kind === "tangent.approval-queue"
        ? { legacy_room_id: "room-a", legacy_envelope_id: "envelope-a" }
        : {}),
    },
  };
}
function renderInbox(path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/" element={<Inbox />} />
        <Route path="/inbox/items/:itemID" element={<Inbox />} />
        <Route path="/r/:roomID" element={<Inbox />} />
      </Routes>
    </MemoryRouter>,
  );
}
beforeEach(() => {
  vi.stubGlobal(
    "EventSource",
    class {
      addEventListener() {}
      close() {}
    },
  );
  fetchInbox.mockResolvedValue([
    entry("approval", 1, "tangent.hitl-item"),
    entry("doc", 2, "tangent.doc-item"),
    entry("turn", 3, "tangent.agent-turn"),
    entry("workflow", 4, "tangent.approval-queue"),
  ]);
});
afterEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

describe("unified Inbox", () => {
  it("interleaves all kinds in global FIFO order and filters without changing it", async () => {
    renderInbox();
    const queue = screen.getByRole("complementary", { name: "Inbox queue" });
    await screen.findByText("Request workflow");
    expect(
      within(queue)
        .getAllByRole("button")
        .map((button) => button.querySelector(".font-medium")?.textContent),
    ).toEqual(["Request approval", "Request doc", "Request turn", "Request workflow"]);
    fireEvent.change(screen.getByLabelText("Interaction type"), { target: { value: "document" } });
    expect(within(queue).getAllByRole("button")).toHaveLength(1);
    expect(screen.getByText("Request doc")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Interaction type"), { target: { value: "all" } });
    fireEvent.change(screen.getByLabelText("Inbox sort"), { target: { value: "newest" } });
    expect(within(queue).getAllByRole("button")[0]).toHaveTextContent("Request workflow");
  });
  it("opens the actual structured body in a flexing pane and can expand it", async () => {
    renderInbox();
    fireEvent.click(await screen.findByText("Request workflow"));
    expect(await screen.findByText("Interactive workflow in room-a")).toBeInTheDocument();
    expect(screen.getByTestId("inbox-body")).toHaveClass("flex-1", "min-h-0");
    fireEvent.click(screen.getByRole("button", { name: "Expand interaction" }));
    expect(screen.getByRole("complementary", { name: "Inbox queue" })).toHaveClass("md:hidden");
    expect(screen.getByRole("button", { name: "Show inbox queue" })).toBeInTheDocument();
  });
  it("preserves the selected answer when a workflow resolves and after reopening", async () => {
    const request = entry("workflow", 4, "tangent.approval-queue");
    fetchInbox.mockResolvedValue([request]);
    const mounted = renderInbox("/inbox/items/workflow");
    await screen.findByText("Interactive workflow in room-a");
    const resolved: InboxEntry = {
      ...request,
      interaction: { ...request.interaction, state: "resolved", revision: 2 },
      resolution: {
        resolution_id: "answer",
        response_payload: { decision: "approved", note: "Ship on Thursday." },
        recorded_at: "2026-10-01T11:00:00Z",
      },
    };
    fetchInbox.mockResolvedValue([resolved]);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Refresh inbox" }));
    });
    expect(await screen.findByText("Your reply")).toBeInTheDocument();
    expect(screen.getByText("Ship on Thursday.")).toBeInTheDocument();
    expect(screen.queryByText("Interactive workflow in room-a")).not.toBeInTheDocument();
    mounted.unmount();
    renderInbox("/inbox/items/workflow");
    expect(await screen.findByText("Ship on Thursday.")).toBeInTheDocument();
    expect(screen.getByText("Original workflow request")).toBeInTheDocument();
  });
  it("opens the latest interaction from a room deep link", async () => {
    renderInbox("/r/room-a");
    expect(await screen.findByText("Interactive workflow in room-a")).toBeInTheDocument();
  });
  it("reports a failed sync without pretending the queue is empty", async () => {
    fetchInbox.mockRejectedValue(new Error("Server unavailable"));
    renderInbox();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Server unavailable"));
  });
});

it("keeps external review history selectable while its renderer is read-only", async () => {
  const review = entry("review-history", 1, "tangent.external-review");
  review.interaction.state = "resolved";
  review.interaction.legacy_room_id = "review-room";
  review.interaction.legacy_envelope_id = "review-envelope";
  fetchInbox.mockResolvedValue([review]);
  renderInbox("/inbox/items/review-history");
  const original = await screen.findByText("Original workflow request");
  const wrapper = original.closest("fieldset");
  expect(wrapper).not.toHaveAttribute("inert");
  expect(wrapper).toHaveAttribute("disabled");
});
