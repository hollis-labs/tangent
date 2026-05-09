import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  BlockDraft,
  type BlockDraftEnvelope,
  type BlockDraftResponse,
} from "../components/envelopes/BlockDraft";
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
