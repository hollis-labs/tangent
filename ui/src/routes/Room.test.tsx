import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  BlockDraft,
  type BlockDraftEnvelope,
  type BlockDraftResponse,
} from "../components/envelopes/BlockDraft";
import {
  Feedback,
  type FeedbackEnvelope,
  type FeedbackResponse,
} from "../components/envelopes/Feedback";
import {
  FormCollect,
  type FormCollectEnvelope,
  type FormCollectResponse,
} from "../components/envelopes/FormCollect";
import {
  OutputRender,
  type OutputRenderEnvelope,
  type OutputRenderResponse,
} from "../components/envelopes/OutputRender";
import {
  ProseRevision,
  type ProseRevisionEnvelope,
  type ProseRevisionResponse,
} from "../components/envelopes/ProseRevision";
import {
  SynthesisNotes,
  type SynthesisNotesEnvelope,
  type SynthesisNotesResponse,
} from "../components/envelopes/SynthesisNotes";
import {
  _resetRegistryForTests,
  type EnvelopeComponentProps,
  register,
} from "../lib/envelope-registry";
import type { ConnectionState, ServerError } from "../lib/ws-client";
import Room from "./Room";

const switchRoom = vi.fn();
const submitResponse = vi.fn(() => true);
const cancel = vi.fn(() => true);
const close = vi.fn();
const claimResolver = vi.fn(() => true);
const releaseResolver = vi.fn(() => true);
const resync = vi.fn(() => true);
const connectMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/ws-client", () => ({
  connect: connectMock,
}));

connectMock.mockImplementation(() => ({
  isConnected: () => true,
  submitResponse,
  cancel,
  close,
  switchRoom,
}));

describe("<Room>", () => {
  afterEach(() => {
    vi.clearAllMocks();
    vi.restoreAllMocks();
    _resetRegistryForTests();
  });

  it("switches rooms on route change without remounting the client", async () => {
    mockFetchForRoom();
    renderAt("/r/room-a", true);
    await screen.findByText("Connecting to the interaction…");

    fireEvent.click(screen.getByTestId("go-room-b"));
    await waitFor(() => {
      expect(switchRoom).toHaveBeenCalledWith("room-b");
    });
  });

  it("does not render stale async enrichment after switching rooms", async () => {
    let onEnvelope: ((id: string, envelope: unknown, revision: number) => void) | null = null;
    connectMock.mockImplementationOnce(
      (
        _roomID: string,
        opts: { onEnvelope: (id: string, envelope: unknown, revision: number) => void },
      ) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );

    let finishRoomA: ((response: Response) => void) | undefined;
    vi.spyOn(globalThis, "fetch").mockReturnValue(
      new Promise<Response>((resolve) => {
        finishRoomA = resolve;
      }),
    );

    renderAt("/r/room-a", true);
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    act(() => {
      onEnvelope?.("env-a", { v: 1, id: "env-a", type: "tangent.dashboard", data: {} }, 1);
    });

    fireEvent.click(screen.getByTestId("go-room-b"));
    await waitFor(() => expect(switchRoom).toHaveBeenCalledWith("room-b"));
    await act(async () => {
      onEnvelope?.(
        "env-b",
        { v: 1, id: "env-b", type: "tangent.triage", data: { items: ["current"] } },
        2,
      );
    });
    expect(await screen.findByText("envelope: env-b")).toBeInTheDocument();

    await act(async () => {
      finishRoomA?.({
        ok: true,
        json: async () => ({ dashboard: {} }),
      } as Response);
    });

    expect(screen.getByText("envelope: env-b")).toBeInTheDocument();
    expect(screen.queryByText("envelope: env-a")).not.toBeInTheDocument();
  });

  it("beforeunload leaves the active envelope unresolved", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );

    mockFetchForRoom();
    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("env-1", { v: 1, id: "env-1", type: "tangent.triage", data: { items: ["a"] } });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalled();
  });

  it("beforeunload does not cancel an active whiteboard envelope", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.whiteboard", WhiteboardProbeAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("whiteboard-refresh-1", {
        v: 1,
        id: "whiteboard-refresh-1",
        type: "tangent.whiteboard",
        data: { board_id: "board-1" },
      });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalledWith("whiteboard-refresh-1");
  });

  it("beforeunload does not cancel an active spreadsheet-review envelope", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.spreadsheet-review", SpreadsheetReviewProbeAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("spreadsheet-refresh-1", {
        v: 1,
        id: "spreadsheet-refresh-1",
        type: "tangent.spreadsheet-review",
        data: { table_id: "table-1" },
      });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalledWith("spreadsheet-refresh-1");
  });

  it("beforeunload does not cancel an active file-picker envelope", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    mockFetchForRoom();
    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("picker-refresh-1", {
        v: 1,
        id: "picker-refresh-1",
        type: "tangent.file-picker",
        data: {
          picker_id: "picker-1",
          browse_roots: [{ root_id: "workspace", path: "/tmp/workspace" }],
        },
      });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalledWith("picker-refresh-1");
  });

  it("beforeunload does not cancel an active dashboard envelope", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );

    mockFetchForRoom();
    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("dashboard-refresh-1", {
        v: 1,
        id: "dashboard-refresh-1",
        type: "tangent.dashboard",
        data: {
          dashboard_id: "dashboard-1",
          tiles: [],
        },
      });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalledWith("dashboard-refresh-1");
  });

  it("beforeunload does not cancel an active approval-queue envelope", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    mockFetchForRoom();
    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("approval-refresh-1", {
        v: 1,
        id: "approval-refresh-1",
        type: "tangent.approval-queue",
        data: { queue_id: "queue-1", items: [{ id: "item-1", title: "Review" }] },
      });
    });

    await act(async () => {
      window.dispatchEvent(new Event("beforeunload"));
    });
    expect(cancel).not.toHaveBeenCalledWith("approval-refresh-1");
  });

  it("enriches synthesis-notes envelopes with the gated session state", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.synthesis-notes", SynthesisAdapter);
    vi.spyOn(globalThis, "fetch").mockResolvedValue({
      ok: true,
      json: async () => ({
        envelopes_history: [],
        synthesis_notes: {
          visibility: "hidden",
          outline_state: "present",
          has_private_notes: true,
        },
      }),
    } as Response);

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("synth-1", { v: 1, id: "synth-1", type: "tangent.synthesis-notes", data: {} });
    });

    expect(await screen.findByTestId("synthesis-notes-hidden")).toHaveTextContent(
      "Private synthesis notes are saved on this room.",
    );
  });

  it("enriches block-draft envelopes with the reconstructed current draft", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.block-draft", BlockDraftAdapter);
    vi.spyOn(globalThis, "fetch").mockResolvedValue({
      ok: true,
      json: async () => ({
        current_draft: {
          block_count: 1,
          markdown: "Accepted block so far.",
          blocks: [{ block_id: "intro", content: "Accepted block so far." }],
        },
      }),
    } as Response);

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("draft-1", {
        v: 1,
        id: "draft-1",
        type: "tangent.block-draft",
        data: { block_id: "body", content: "New candidate block." },
      });
    });

    expect(await screen.findByTestId("block-draft-current-draft")).toHaveTextContent(
      "Accepted block so far.",
    );
  });

  it("enriches prose-revision envelopes with the reconstructed current draft", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.prose-revision", ProseRevisionAdapter);
    vi.spyOn(globalThis, "fetch").mockResolvedValue({
      ok: true,
      json: async () => ({
        current_draft: {
          block_count: 1,
          markdown: "Accepted block so far.",
          blocks: [{ block_id: "intro", content: "Accepted block so far." }],
        },
      }),
    } as Response);

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("rev-1", {
        v: 1,
        id: "rev-1",
        type: "tangent.prose-revision",
        data: {
          lens: "copy",
          source_text: "Candidate paragraph.",
          suggestions: [{ id: "s1", suggested_text: "Tighter paragraph." }],
        },
      });
    });

    expect(await screen.findByTestId("prose-revision-current-draft")).toHaveTextContent(
      "Accepted block so far.",
    );
  });

  it("enriches output-render envelopes with the persisted final output", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.output-render", OutputRenderAdapter);
    vi.spyOn(globalThis, "fetch").mockResolvedValue({
      ok: true,
      json: async () => ({
        final_output: {
          markdown: "# Final output\n\nAccepted final copy.",
          filename: "final.md",
          format: "markdown",
        },
      }),
    } as Response);

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("output-1", {
        v: 1,
        id: "output-1",
        type: "tangent.output-render",
        data: {},
      });
    });

    expect(await screen.findByTestId("output-render-markdown")).toHaveTextContent(
      "Accepted final copy.",
    );
  });

  it("enriches whiteboard envelopes with the latest persisted revision before submit", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.whiteboard", WhiteboardProbeAdapter);
    vi.spyOn(globalThis, "fetch").mockResolvedValue({
      ok: true,
      json: async () => ({
        whiteboard: {
          board_id: "board-1",
          scene_snapshot: { document: { pages: [{ id: "page:persisted" }] } },
          assets: [
            {
              artifact_id: "artifact-1",
              uri: "artifact://artifact-1",
              source: "https://assets.example.test/reference.png",
              kind: "reference_image",
            },
          ],
          export_refs: [{ kind: "png", name: "board-1-r2.png" }],
          notes: "Persisted board notes",
          updated_at: "2026-05-09T20:15:00Z",
          revision_history: [{ revision_id: "board-1-r1" }, { revision_id: "board-1-r2" }],
        },
      }),
    } as Response);

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("whiteboard-1", {
        v: 1,
        id: "whiteboard-1",
        type: "tangent.whiteboard",
        data: {
          board_id: "board-1",
          scene: { document: { pages: [{ id: "page:stale" }] } },
          notes: "stale notes",
        },
      });
    });

    expect(await screen.findByTestId("whiteboard-probe-state")).toHaveTextContent(
      '"revision_id":"board-1-r2"',
    );
    expect(screen.getByTestId("whiteboard-probe-state")).toHaveTextContent(
      '"notes":"Persisted board notes"',
    );
    expect(screen.getByTestId("whiteboard-probe-state")).toHaveTextContent(
      '"reference_images":[{"artifact_id":"artifact-1"',
    );
    expect(screen.getByTestId("whiteboard-probe-state")).toHaveTextContent(
      '"export_refs":[{"kind":"png","name":"board-1-r2.png"}]',
    );

    fireEvent.click(screen.getByTestId("whiteboard-probe-submit"));
    expect(submitResponse).toHaveBeenCalledWith("whiteboard-1", { ok: true });
  });

  it("whiteboard cancel uses the explicit cancel transport", async () => {
    let onEnvelope: ((id: string, envelope: unknown) => void) | null = null;
    connectMock.mockImplementationOnce(
      (_roomID: string, opts: { onEnvelope: (id: string, envelope: unknown) => void }) => {
        onEnvelope = opts.onEnvelope;
        return {
          isConnected: () => true,
          submitResponse,
          cancel,
          close,
          switchRoom,
        };
      },
    );
    register("tangent.whiteboard", WhiteboardProbeAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(onEnvelope).not.toBeNull());
    await act(async () => {
      onEnvelope?.("whiteboard-cancel-1", {
        v: 1,
        id: "whiteboard-cancel-1",
        type: "tangent.whiteboard",
        data: { board_id: "board-1" },
      });
    });

    fireEvent.click(await screen.findByTestId("whiteboard-probe-cancel"));
    expect(cancel).toHaveBeenCalledWith("whiteboard-cancel-1");
  });
});

// CW-20260905-0016. These mount a real workflow renderer, on purpose.
//
// `room-lifecycle.test.ts` already proves `receiveServerError` restores the
// envelope it optimistically cleared, and it passed throughout the bug's life:
// what a lifecycle test cannot see is that Room rendered that clear as an
// unmount, so by the time the envelope came back the component holding the
// operator's answers had been torn down and re-seeded from `envelope.data`.
// The values have to be read back out of live inputs for the defect to exist
// at all, so every test here does.
describe("<Room> submissions the server refuses", () => {
  afterEach(() => {
    vi.clearAllMocks();
    vi.restoreAllMocks();
    _resetRegistryForTests();
    window.localStorage.clear();
  });

  it("returns the operator to their filled-in form, not a blank one", async () => {
    const handlers = mockConnectOnce();
    register("tangent.form-collect", FormCollectAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(handlers.onEnvelope).not.toBeNull());
    await act(async () => {
      handlers.onEnvelope?.("form-1", formCollectEnvelope("form-1"), 1);
    });

    fireEvent.change(await screen.findByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.change(screen.getByTestId("form-collect-notes"), {
      target: { value: "Ship on Thursday." },
    });
    fireEvent.click(screen.getByTestId("form-collect-submit"));
    expect(submitResponse).toHaveBeenCalledWith("form-1", expect.anything(), 1);

    // While the answer is outstanding the pane reads as cleared — the operator
    // sees the same thing they always did — but the component is still there.
    expect(screen.getByTestId("form-collect-root")).not.toBeVisible();
    expect(screen.getByText("Sending your response…")).toBeInTheDocument();

    await act(async () => {
      handlers.onServerError?.({
        code: "resolver_lease_held",
        message: "another connection holds the resolver lease",
        envelopeId: "form-1",
        lease: { connection_id: "conn-1", label: "client 1" },
      });
    });

    expect(screen.getByTestId("form-collect-root")).toBeVisible();
    expect(screen.getByTestId("form-collect-input-headline")).toHaveValue("Launch week");
    expect(screen.getByTestId("form-collect-notes")).toHaveValue("Ship on Thursday.");
    expect(screen.getByTestId("connection-error")).toHaveTextContent(
      "Not submitted: client 1 holds the resolver lease. Take over to answer here.",
    );
  });

  it("keeps the answers through a take-over after a refusal", async () => {
    const handlers = mockConnectOnce();
    register("tangent.form-collect", FormCollectAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(handlers.onEnvelope).not.toBeNull());
    await act(async () => {
      handlers.onEnvelope?.("form-2", formCollectEnvelope("form-2"), 1);
    });

    fireEvent.change(await screen.findByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.click(screen.getByTestId("form-collect-submit"));

    await act(async () => {
      handlers.onConnectionState?.(observerState());
      handlers.onServerError?.({
        code: "resolver_lease_held",
        message: "another connection holds the resolver lease",
        envelopeId: "form-2",
        lease: { connection_id: "conn-1", label: "client 1" },
      });
    });

    fireEvent.click(screen.getByTestId("connection-take-over"));
    expect(claimResolver).toHaveBeenCalledWith(true);

    await act(async () => {
      handlers.onConnectionState?.(resolverState());
    });

    expect(screen.queryByTestId("connection-error")).not.toBeInTheDocument();
    expect(screen.getByTestId("form-collect-input-headline")).toHaveValue("Launch week");
  });

  it("clears the pane when the server does not refuse the submission", async () => {
    const handlers = mockConnectOnce();
    register("tangent.form-collect", FormCollectAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(handlers.onEnvelope).not.toBeNull());
    await act(async () => {
      handlers.onEnvelope?.("form-3", formCollectEnvelope("form-3"), 1);
    });

    fireEvent.change(await screen.findByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.click(screen.getByTestId("form-collect-submit"));

    expect(screen.getByText("Sending your response…")).toBeInTheDocument();
    expect(screen.getByTestId("form-collect-root")).not.toBeVisible();
    expect(screen.queryByRole("textbox", { name: /headline/i })).not.toBeInTheDocument();
  });

  // The other half of the same change. Keeping the renderer mounted removed the
  // remount that was quietly re-seeding every workflow between envelopes, and
  // `Feedback` is the one that never re-initialises `answers` from a changed
  // prop (CW-20260904-0141 item 5). Without the envelope-id key on the router,
  // the second envelope here renders the first one's answer.
  it("does not carry one envelope's answers into the next", async () => {
    const handlers = mockConnectOnce();
    register("tangent.feedback", FeedbackAdapter);
    mockFetchForRoom();

    renderAt("/r/room-a");
    await waitFor(() => expect(handlers.onEnvelope).not.toBeNull());
    await act(async () => {
      handlers.onEnvelope?.("fb-1", feedbackEnvelope("fb-1"), 1);
    });

    fireEvent.change(await screen.findByTestId("feedback-input-q1"), {
      target: { value: "The first envelope's answer" },
    });
    fireEvent.click(screen.getByTestId("feedback-submit"));

    await act(async () => {
      handlers.onEnvelope?.("fb-2", feedbackEnvelope("fb-2"), 2);
    });

    expect(screen.getByText("envelope: fb-2")).toBeInTheDocument();
    expect(screen.getByTestId("feedback-root")).toBeVisible();
    expect(screen.getByTestId("feedback-input-q1")).toHaveValue("");
  });
});

function renderAt(path: string, includeNavigator = false) {
  return render(router(path, includeNavigator));
}

function router(path: string, includeNavigator = false) {
  return (
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/r/:roomID"
          element={
            <>
              {includeNavigator ? <NavigateProbe /> : null}
              <Room />
            </>
          }
        />
      </Routes>
    </MemoryRouter>
  );
}

function mockFetchForRoom() {
  vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true,
    json: async () => ({ envelopes_history: [] }),
  } as Response);
}

function NavigateProbe() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate("/r/room-b")} data-testid="go-room-b">
      switch
    </button>
  );
}

function SynthesisAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <SynthesisNotes
      envelope={envelope as SynthesisNotesEnvelope}
      onSubmit={onSubmit as (response: SynthesisNotesResponse) => void}
      onCancel={onCancel}
    />
  );
}

function BlockDraftAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <BlockDraft
      envelope={envelope as BlockDraftEnvelope}
      onSubmit={onSubmit as (response: BlockDraftResponse) => void}
      onCancel={onCancel}
    />
  );
}

function ProseRevisionAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <ProseRevision
      envelope={envelope as ProseRevisionEnvelope}
      onSubmit={onSubmit as (response: ProseRevisionResponse) => void}
      onCancel={onCancel}
    />
  );
}

function OutputRenderAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <OutputRender
      envelope={envelope as OutputRenderEnvelope}
      onSubmit={onSubmit as (response: OutputRenderResponse) => void}
      onCancel={onCancel}
    />
  );
}

function WhiteboardProbeAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <div>
      <pre data-testid="whiteboard-probe-state">
        {JSON.stringify((envelope as { data?: unknown }).data ?? null)}
      </pre>
      <button
        type="button"
        data-testid="whiteboard-probe-submit"
        onClick={() => onSubmit({ ok: true })}
      >
        submit
      </button>
      <button type="button" data-testid="whiteboard-probe-cancel" onClick={onCancel}>
        cancel
      </button>
    </div>
  );
}

function FormCollectAdapter({ envelope, onSubmit, onCancel, roomID }: EnvelopeComponentProps) {
  return (
    <FormCollect
      envelope={envelope as FormCollectEnvelope}
      onSubmit={onSubmit as (response: FormCollectResponse) => void}
      onCancel={onCancel}
      roomID={roomID}
    />
  );
}

function FeedbackAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <Feedback
      envelope={envelope as FeedbackEnvelope}
      onSubmit={onSubmit as (response: FeedbackResponse) => void}
      onCancel={onCancel}
    />
  );
}

function formCollectEnvelope(id: string): FormCollectEnvelope {
  return {
    v: 1,
    id,
    type: "tangent.form-collect",
    title: "Collect launch facts",
    data: {
      form_id: id,
      schema: { fields: [{ id: "headline", type: "text", label: "Headline", required: true }] },
    },
  };
}

function feedbackEnvelope(id: string): FeedbackEnvelope {
  return {
    v: 1,
    id,
    type: "tangent.feedback",
    title: "How did that go?",
    data: { questions: [{ id: "q1", type: "text", label: "What went well?" }] },
  };
}

// The connection frames a losing tab and then a winning tab receive, with this
// tab as `self` in both so ConnectionStatus renders the right control.
function observerState(): ConnectionState {
  return {
    connectionId: "conn-2",
    role: "observer",
    lease: { connection_id: "conn-1", label: "client 1" },
    connections: [
      { connection_id: "conn-1", label: "client 1", role: "resolver" },
      { connection_id: "conn-2", role: "observer", self: true },
    ],
  };
}

function resolverState(): ConnectionState {
  return {
    connectionId: "conn-2",
    role: "resolver",
    lease: { connection_id: "conn-2" },
    connections: [
      { connection_id: "conn-1", label: "client 1", role: "observer" },
      { connection_id: "conn-2", role: "resolver", self: true },
    ],
  };
}

type CapturedHandlers = {
  onEnvelope: ((id: string, envelope: unknown, revision: number) => void) | null;
  onServerError: ((error: ServerError) => void) | null;
  onConnectionState: ((state: ConnectionState) => void) | null;
};

// Captures the server-facing callbacks Room registers, so a test can play the
// server: present an envelope, refuse a submission, hand the lease around.
function mockConnectOnce(): CapturedHandlers {
  const handlers: CapturedHandlers = {
    onEnvelope: null,
    onServerError: null,
    onConnectionState: null,
  };
  connectMock.mockImplementationOnce(
    (
      _roomID: string,
      opts: {
        onEnvelope: (id: string, envelope: unknown, revision: number) => void;
        onServerError?: (error: ServerError) => void;
        onConnectionState?: (state: ConnectionState) => void;
      },
    ) => {
      handlers.onEnvelope = opts.onEnvelope;
      handlers.onServerError = opts.onServerError ?? null;
      handlers.onConnectionState = opts.onConnectionState ?? null;
      return {
        isConnected: () => true,
        submitResponse,
        cancel,
        close,
        switchRoom,
        claimResolver,
        releaseResolver,
        resync,
      };
    },
  );
  return handlers;
}

function SpreadsheetReviewProbeAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  return (
    <div>
      <pre data-testid="spreadsheet-review-probe-state">
        {JSON.stringify((envelope as { data?: unknown }).data ?? null)}
      </pre>
      <button
        type="button"
        data-testid="spreadsheet-review-probe-submit"
        onClick={() => onSubmit({ ok: true })}
      >
        submit
      </button>
      <button type="button" data-testid="spreadsheet-review-probe-cancel" onClick={onCancel}>
        cancel
      </button>
    </div>
  );
}
