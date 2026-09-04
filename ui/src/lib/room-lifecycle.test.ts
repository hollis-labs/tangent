import { describe, expect, it, vi } from "vitest";

import { createRoomLifecycle } from "./room-lifecycle";
import type { WSClient } from "./ws-client";

describe("room lifecycle", () => {
  it("retains the active presentation when response or cancel cannot be sent", () => {
    const submitResponse = vi.fn(() => false);
    const cancel = vi.fn(() => false);
    const client: WSClient = {
      isConnected: () => false,
      submitResponse,
      cancel,
      switchRoom: vi.fn(),
      close: vi.fn(),
    };
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
});
