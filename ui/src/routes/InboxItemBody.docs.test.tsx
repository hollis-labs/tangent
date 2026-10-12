import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import type { DocItemView } from "@/lib/docs-api";
import type { InboxEntry } from "@/lib/inbox-api";
import { InboxItemBody } from "./InboxItemBody";

const doc: DocItemView = {
  contract_version: "1.0",
  item_id: "doc-item-42",
  agent_id: "agent",
  title: "A document",
  content_markdown: "body text",
  requires_ack: false,
  state: "presented",
  queue_sequence: 1,
  revision: 1,
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};
const entry: InboxEntry = {
  sequence: 1,
  interaction: {
    interaction_id: doc.item_id,
    definition_binding: { kind: "tangent.doc-item" },
    request_snapshot: {},
    caller_scope: "test",
    state: "presented",
    revision: 1,
    created_at: doc.created_at,
    updated_at: doc.updated_at,
  },
};

afterEach(() => vi.unstubAllGlobals());

function mount(writeText: (text: string) => Promise<void>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify(doc), { headers: { "Content-Type": "application/json" } }),
    ),
  );
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(
    <MemoryRouter>
      <InboxItemBody entry={entry} onChange={async () => {}} />
    </MemoryRouter>,
  );
}

it("copies the document's object id from the actions bar", async () => {
  const writeText = vi.fn(async () => {});
  mount(writeText);
  fireEvent.click(await screen.findByRole("button", { name: "Copy object id" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith("doc-item-42"));
  expect(await screen.findByText("Object id copied")).toBeInTheDocument();
});

it("says so when the clipboard refuses", async () => {
  mount(async () => Promise.reject(new Error("denied")));
  fireEvent.click(await screen.findByRole("button", { name: "Copy object id" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not copy the object id");
});
