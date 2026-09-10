import { describe, expect, it, vi } from "vitest";

import { createRoomLifecycle } from "./room-lifecycle";
import type { ConnectionState, WSClient } from "./ws-client";

function stubClient(overrides: Partial<WSClient> = {}): WSClient {
  return {
    isConnected: () => false,
    submitResponse: vi.fn(() => true),
    cancel: vi.fn(() => true),
    saveDraft: vi.fn(() => true),
    claimResolver: vi.fn(() => true),
    releaseResolver: vi.fn(() => true),
    resync: vi.fn(() => true),
    switchRoom: vi.fn(),
    clientID: () => "tab-1",
    close: vi.fn(),
    ...overrides,
  };
}

function connectionState(role: "resolver" | "observer"): ConnectionState {
  return {
    connectionId: "conn-self",
    role,
    roomID: "room-a",
    lease:
      role === "resolver"
        ? { connection_id: "conn-self", label: "this tab" }
        : { connection_id: "conn-peer", label: "tab a" },
    connections: [
      { connection_id: "conn-self", role, self: true },
      { connection_id: "conn-peer", role: role === "resolver" ? "observer" : "resolver" },
    ],
  };
}

describe("room lifecycle", () => {
  // The draft path. ADR 0007 §5: recording what a participant is looking at is
  // a strictly lesser act than deciding, and the lifecycle has to keep it that
  // way — a draft that cleared `pending` would blank the operator's pane the
  // moment they touched a filter.
  it("saves a draft against the pending envelope without settling it", () => {
    const saveDraft = vi.fn(() => true);
    const submitResponse = vi.fn(() => true);
    const client = stubClient({ saveDraft, submitResponse });
    const lifecycle = createRoomLifecycle("room-a", client);
    lifecycle.receiveEnvelope("env-1", { type: "tangent.app-board" }, 4);

    expect(lifecycle.saveDraft({ filters: { status: ["doing"] } })).toBe(true);

    // The envelope id comes from the lifecycle, and the revision is NOT passed:
    // ws-client owns that counter because the store computes MAX(revision) + 1
    // and refuses anything else. A caller-supplied revision was a real defect
    // and must not come back.
    expect(saveDraft).toHaveBeenCalledWith("env-1", { filters: { status: ["doing"] } });
    expect(saveDraft.mock.calls[0]).toHaveLength(2);
    expect(submitResponse).not.toHaveBeenCalled();
    expect(lifecycle.activeEnvelope()).toEqual({
      envelopeId: "env-1",
      envelope: { type: "tangent.app-board" },
      revision: 4,
    });
  });

  it("refuses a draft when nothing is pending", () => {
    const saveDraft = vi.fn(() => true);
    const lifecycle = createRoomLifecycle("room-a", stubClient({ saveDraft }));

    // A draft has no interaction to belong to before an envelope arrives, and
    // none after one is submitted.
    expect(lifecycle.saveDraft({ filters: {} })).toBe(false);
    lifecycle.receiveEnvelope("env-1", { type: "tangent.app-board" }, 1);
    lifecycle.submit({ kind: "data" });
    expect(lifecycle.saveDraft({ filters: {} })).toBe(false);
    expect(saveDraft).not.toHaveBeenCalled();
  });

  it("reports a draft the transport could not send", () => {
    const lifecycle = createRoomLifecycle("room-a", stubClient({ saveDraft: vi.fn(() => false) }));
    lifecycle.receiveEnvelope("env-1", { type: "tangent.app-board" }, 1);

    expect(lifecycle.saveDraft({ filters: {} })).toBe(false);
    // A failed draft still leaves the operator looking at their envelope.
    expect(lifecycle.activeEnvelope()?.envelopeId).toBe("env-1");
  });

  it("retains the active presentation when response or cancel cannot be sent", () => {
    const submitResponse = vi.fn(() => false);
    const cancel = vi.fn(() => false);
    const client = stubClient({ submitResponse, cancel });
    const lifecycle = createRoomLifecycle("room-a", client);
    lifecycle.receiveEnvelope("env-1", { type: "tangent.triage" }, 4);

    expect(lifecycle.submit({ ok: true })).toBe(false);
    expect(lifecycle.activeEnvelope()).toMatchObject({ envelopeId: "env-1", revision: 4 });
    expect(lifecycle.cancel()).toBe(false);
    expect(lifecycle.activeEnvelope()).toMatchObject({ envelopeId: "env-1", revision: 4 });

    cancel.mockReturnValue(true);
    expect(lifecycle.cancel()).toBe(true);
    expect(lifecycle.activeEnvelope()).toBeNull();
  });

  it("tracks connection state independently of the presented envelope", () => {
    const lifecycle = createRoomLifecycle("room-a", stubClient());

    // Connection state exists before anything is presented.
    expect(lifecycle.connectionState()).toBeNull();
    lifecycle.receiveConnectionState(connectionState("observer"));
    expect(lifecycle.connectionState()?.role).toBe("observer");
    expect(lifecycle.canResolve()).toBe(false);
    expect(lifecycle.activeEnvelope()).toBeNull();

    // Presenting an envelope leaves connection state exactly as it was.
    lifecycle.receiveEnvelope("env-1", { type: "tangent.triage" }, 2);
    expect(lifecycle.connectionState()?.role).toBe("observer");
    expect(lifecycle.activeEnvelope()).toMatchObject({ envelopeId: "env-1" });

    lifecycle.receiveConnectionState(connectionState("resolver"));
    expect(lifecycle.canResolve()).toBe(true);
    expect(lifecycle.activeEnvelope()).toMatchObject({ envelopeId: "env-1" });
  });

  it("restores the envelope a refused submission optimistically cleared", () => {
    const lifecycle = createRoomLifecycle("room-a", stubClient());
    lifecycle.receiveConnectionState(connectionState("observer"));
    lifecycle.receiveEnvelope("env-1", { type: "tangent.triage" }, 3);

    expect(lifecycle.submit({ ok: true })).toBe(true);
    expect(lifecycle.activeEnvelope()).toBeNull();

    lifecycle.receiveServerError({
      code: "resolver_lease_held",
      message: "held by tab a",
      envelopeId: "env-1",
      lease: { connection_id: "conn-peer", label: "tab a" },
    });
    // The submission did not happen, so the operator gets their envelope back
    // together with the reason.
    expect(lifecycle.activeEnvelope()).toMatchObject({ envelopeId: "env-1", revision: 3 });
    expect(lifecycle.lastServerError()?.code).toBe("resolver_lease_held");

    // Winning the lease clears the complaint.
    lifecycle.receiveConnectionState(connectionState("resolver"));
    expect(lifecycle.lastServerError()).toBeNull();
  });

  it("clears connection, sync, and refusal state when the room changes", () => {
    const switchRoom = vi.fn();
    const lifecycle = createRoomLifecycle("room-a", stubClient({ switchRoom }));
    lifecycle.receiveConnectionState(connectionState("resolver"));
    lifecycle.receiveSync({ room_id: "room-a", surface_revision: 5, presentations: [] });
    lifecycle.receiveEnvelope("env-1", { type: "tangent.triage" }, 1);

    expect(lifecycle.switchRoom("room-b")).toBe(true);
    expect(switchRoom).toHaveBeenCalledWith("room-b");
    expect(lifecycle.connectionState()).toBeNull();
    expect(lifecycle.sync()).toBeNull();
    expect(lifecycle.activeEnvelope()).toBeNull();
    expect(lifecycle.lastServerError()).toBeNull();
  });

  it("records the durable revisions it is synchronized to", () => {
    const lifecycle = createRoomLifecycle("room-a", stubClient());
    lifecycle.receiveSync({
      room_id: "room-a",
      surface_revision: 11,
      presentations: [
        { envelope_id: "env-1", durable: true, interaction_id: "int-1", interaction_revision: 4 },
      ],
    });
    expect(lifecycle.sync()?.surface_revision).toBe(11);
    expect(lifecycle.sync()?.presentations[0].interaction_id).toBe("int-1");
  });
});
