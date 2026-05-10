import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  BlockDraft,
  type BlockDraftEnvelope,
  type BlockDraftResponse,
} from "../components/envelopes/BlockDraft";
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
import Room from "./Room";

const switchRoom = vi.fn();
const submitResponse = vi.fn();
const cancel = vi.fn();
const close = vi.fn();
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
    await screen.findByText("waiting for envelope...");

    fireEvent.click(screen.getByTestId("go-room-b"));
    await waitFor(() => {
      expect(switchRoom).toHaveBeenCalledWith("room-b");
    });
  });

  it("beforeunload cancels the active envelope", async () => {
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
    expect(cancel).toHaveBeenCalledWith("env-1");
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
        result: {
          content: [
            {
              text: JSON.stringify({
                envelopes_history: [],
                synthesis_notes: {
                  visibility: "hidden",
                  outline_state: "present",
                  has_private_notes: true,
                },
              }),
            },
          ],
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
        result: {
          content: [
            {
              text: JSON.stringify({
                current_draft: {
                  block_count: 1,
                  markdown: "Accepted block so far.",
                  blocks: [{ block_id: "intro", content: "Accepted block so far." }],
                },
              }),
            },
          ],
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
        result: {
          content: [
            {
              text: JSON.stringify({
                current_draft: {
                  block_count: 1,
                  markdown: "Accepted block so far.",
                  blocks: [{ block_id: "intro", content: "Accepted block so far." }],
                },
              }),
            },
          ],
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
        result: {
          content: [
            {
              text: JSON.stringify({
                final_output: {
                  markdown: "# Final output\n\nAccepted final copy.",
                  filename: "final.md",
                  format: "markdown",
                },
              }),
            },
          ],
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
        result: {
          content: [
            {
              text: JSON.stringify({
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
            },
          ],
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
    json: async () => ({
      result: {
        content: [{ text: JSON.stringify({ envelopes_history: [] }) }],
      },
    }),
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
