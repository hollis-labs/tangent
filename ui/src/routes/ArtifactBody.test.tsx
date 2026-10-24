import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ArtifactView } from "@/lib/artifacts-api";
import type { InboxEntry } from "@/lib/inbox-api";
import { InboxItemBody } from "./InboxItemBody";

let announce: () => void = () => {};
vi.mock("@/lib/event-hub", () => ({
  subscribeRevision: (_url: string, listener: () => void) => {
    announce = listener;
    return { close: () => {} };
  },
}));

function artifact(overrides: Partial<ArtifactView> = {}): ArtifactView {
  return {
    contract_version: "1.0",
    item_id: "art-1",
    agent_id: "designer",
    agent_label: "Designer",
    title: "Landing page",
    type: "html",
    requires_signoff: true,
    state: "presented",
    queue_sequence: 1,
    revision: 3,
    created_at: "2026-10-24T10:00:00Z",
    updated_at: "2026-10-24T10:00:00Z",
    revisions: [
      {
        revision: 1,
        filename: "artifact.html",
        media_type: "text/html",
        size_bytes: 10,
        sha256: "a",
        created_at: "2026-10-24T10:00:00Z",
      },
    ],
    latest_revision: 1,
    messages: [],
    inbox_path: "/inbox/items/art-1",
    ...overrides,
  };
}

const entry: InboxEntry = {
  sequence: 1,
  interaction: {
    interaction_id: "art-1",
    definition_binding: { kind: "tangent.artifact-item" },
    request_snapshot: {},
    caller_scope: "test",
    state: "presented",
    revision: 3,
    created_at: "2026-10-24T10:00:00Z",
    updated_at: "2026-10-24T10:00:00Z",
  },
};

let current: ArtifactView;
const calls: { url: string; method: string; body?: string }[] = [];

type HappyWindow = Window & { happyDOM?: { settings: { disableIframePageLoading: boolean } } };

beforeEach(() => {
  // Keep happy-dom from really loading the iframe URL.
  const happy = (window as HappyWindow).happyDOM;
  if (happy) happy.settings.disableIframePageLoading = true;
  current = artifact();
  calls.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? "GET", body: init?.body as string | undefined });
      return new Response(JSON.stringify(current), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function mount() {
  render(
    <MemoryRouter>
      <InboxItemBody entry={entry} onChange={async () => {}} />
    </MemoryRouter>,
  );
}

it("shows an HTML artifact in a frame with no same-origin grant", async () => {
  mount();
  const frame = await screen.findByTestId("artifact-html-frame");
  expect(frame).toHaveAttribute("src", "/api/artifacts/items/art-1/revisions/1/content");
  const sandbox = frame.getAttribute("sandbox") ?? "";
  expect(sandbox).toContain("allow-scripts");
  expect(sandbox).not.toContain("allow-same-origin");
});

it("follows a new revision from the agent and says so", async () => {
  mount();
  await screen.findByTestId("artifact-html-frame");
  current = artifact({
    latest_revision: 2,
    revisions: [
      ...current.revisions,
      {
        revision: 2,
        filename: "artifact.html",
        media_type: "text/html",
        size_bytes: 12,
        sha256: "b",
        note: "bigger heading",
        created_at: "2026-10-24T10:05:00Z",
      },
    ],
  });
  announce();
  expect(await screen.findByText("Updated to revision 2")).toBeInTheDocument();
  expect(screen.getByTestId("artifact-html-frame")).toHaveAttribute(
    "src",
    "/api/artifacts/items/art-1/revisions/2/content",
  );
  expect(screen.getByText(/bigger heading/)).toBeInTheDocument();
});

it("keeps the revision the operator picked when the agent revises again", async () => {
  current = artifact({
    latest_revision: 2,
    revisions: [
      {
        revision: 1,
        filename: "a.html",
        media_type: "text/html",
        size_bytes: 1,
        sha256: "a",
        created_at: "2026-10-24T10:00:00Z",
      },
      {
        revision: 2,
        filename: "a.html",
        media_type: "text/html",
        size_bytes: 1,
        sha256: "b",
        created_at: "2026-10-24T10:01:00Z",
      },
    ],
  });
  mount();
  await screen.findByTestId("artifact-html-frame");
  fireEvent.click(screen.getByRole("button", { name: "Previous revision" }));
  expect(screen.getByTestId("artifact-html-frame")).toHaveAttribute(
    "src",
    "/api/artifacts/items/art-1/revisions/1/content",
  );
  expect(screen.getByText("Revision 1 of 2")).toBeInTheDocument();
});

it("maximizes the preview and leaves with Escape", async () => {
  mount();
  await screen.findByTestId("artifact-html-frame");
  fireEvent.click(screen.getByRole("button", { name: "Maximize preview" }));
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});

it("offers a download of the shown revision and a direct link", async () => {
  mount();
  const download = await screen.findByRole("link", { name: "Download artifact" });
  expect(download).toHaveAttribute(
    "href",
    "/api/artifacts/items/art-1/revisions/1/content?download=1",
  );
  expect(screen.getByRole("button", { name: "Copy direct link" })).toBeInTheDocument();
});

it("sends a request-changes note and offers approval only when sign-off was requested", async () => {
  mount();
  fireEvent.change(await screen.findByLabelText(/Message to Designer/), {
    target: { value: "Make it blue" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/messages"))).toBe(true),
  );
  const post = calls.find((c) => c.url.endsWith("/messages"));
  expect(JSON.parse(post?.body ?? "{}")).toEqual({
    body: "Make it blue",
    kind: "changes_requested",
  });
  expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
});

it("hides Approve when no sign-off was requested", async () => {
  current = artifact({ requires_signoff: false });
  mount();
  await screen.findByLabelText(/Message to Designer/);
  expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
});

it("closes the thread once the artifact is finished", async () => {
  current = artifact({
    state: "resolved",
    resolution: {
      resolution_id: "r",
      action: "approved",
      revision: 1,
      resolved_at: "2026-10-24T11:00:00Z",
      resolved_by: "operator",
    },
  });
  mount();
  expect(await screen.findByText(/Approved revision 1/)).toBeInTheDocument();
  expect(screen.queryByLabelText(/Message to Designer/)).not.toBeInTheDocument();
});

it("shows the source for a type that has no rendered preview yet", async () => {
  current = artifact({
    type: "d2",
    revisions: [
      {
        revision: 1,
        filename: "a.d2",
        media_type: "application/json",
        size_bytes: 3,
        sha256: "a",
        created_at: "2026-10-24T10:00:00Z",
      },
    ],
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      url.endsWith("/content")
        ? new Response("a -> b")
        : new Response(JSON.stringify(current), {
            headers: { "Content-Type": "application/json" },
          }),
    ),
  );
  mount();
  expect(
    await screen.findByText(/rendered preview for this type is not available/),
  ).toBeInTheDocument();
  expect(await screen.findByText("a -> b")).toBeInTheDocument();
});

it("shows the server-drawn picture for a D2 revision that has one", async () => {
  current = artifact({
    type: "d2",
    revisions: [
      {
        revision: 1,
        filename: "a.d2",
        media_type: "text/plain",
        size_bytes: 6,
        sha256: "a",
        rendered: true,
        created_at: "2026-10-24T10:00:00Z",
      },
    ],
  });
  mount();
  const image = await screen.findByTestId("artifact-rendered");
  expect(image).toHaveAttribute("src", "/api/artifacts/items/art-1/revisions/1/rendered");
});
