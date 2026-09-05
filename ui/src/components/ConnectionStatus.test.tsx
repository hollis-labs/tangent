import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ConnectionState } from "@/lib/ws-client";
import { ConnectionStatus } from "./ConnectionStatus";

function observerState(): ConnectionState {
  return {
    connectionId: "conn-self",
    role: "observer",
    roomID: "room-a",
    lease: { connection_id: "conn-peer", label: "tab a" },
    connections: [
      { connection_id: "conn-self", role: "observer", label: "tab b", self: true },
      { connection_id: "conn-peer", role: "resolver", label: "tab a" },
    ],
  };
}

describe("ConnectionStatus", () => {
  it("shows connection state with nothing presented", () => {
    render(
      <ConnectionStatus
        transport="connected"
        connection={{
          connectionId: "conn-self",
          role: "resolver",
          lease: { connection_id: "conn-self", label: "tab a" },
          connections: [{ connection_id: "conn-self", role: "resolver", self: true }],
        }}
        sync={{ room_id: "room-a", surface_revision: 7, presentations: [] }}
        serverError={null}
        onTakeOver={vi.fn()}
        onRelease={vi.fn()}
        onResync={vi.fn()}
      />,
    );

    expect(screen.getByTestId("connection-transport")).toHaveTextContent("connected");
    expect(screen.getByTestId("connection-role")).toHaveTextContent("resolver");
    expect(screen.getByTestId("connection-peers")).toHaveTextContent("no other clients attached");
    expect(screen.getByTestId("connection-sync")).toHaveTextContent("durable rev 7");
    expect(screen.queryByTestId("connection-take-over")).toBeNull();
  });

  it("names the tab holding the resolver lease and offers a takeover", async () => {
    const onTakeOver = vi.fn();
    render(
      <ConnectionStatus
        transport="connected"
        connection={observerState()}
        sync={null}
        serverError={null}
        onTakeOver={onTakeOver}
        onRelease={vi.fn()}
        onResync={vi.fn()}
      />,
    );

    expect(screen.getByTestId("connection-role")).toHaveTextContent("observer");
    expect(screen.getByTestId("connection-peers")).toHaveTextContent("1 other client attached");
    expect(screen.getByTestId("connection-lease-holder")).toHaveTextContent(
      "tab a is resolving this surface.",
    );
    expect(screen.getByTestId("connection-peer-list")).toHaveTextContent("tab a — resolver");

    screen.getByTestId("connection-take-over").click();
    expect(onTakeOver).toHaveBeenCalledTimes(1);
  });

  it("explains a refused submission rather than leaving it silent", () => {
    render(
      <ConnectionStatus
        transport="connected"
        connection={observerState()}
        sync={null}
        serverError={{
          code: "resolver_lease_held",
          message: "room: resolver lease held by another connection",
          envelopeId: "env-1",
          lease: { connection_id: "conn-peer", label: "tab a" },
        }}
        onTakeOver={vi.fn()}
        onRelease={vi.fn()}
        onResync={vi.fn()}
      />,
    );

    expect(screen.getByTestId("connection-error")).toHaveTextContent(
      "Not submitted: tab a holds the resolver lease.",
    );
  });

  it("explains a stale presentation refusal", () => {
    render(
      <ConnectionStatus
        transport="connected"
        connection={observerState()}
        sync={null}
        serverError={{
          code: "stale_presentation",
          message: "stale",
          envelopeId: "env-1",
          lease: null,
        }}
        onTakeOver={vi.fn()}
        onRelease={vi.fn()}
        onResync={vi.fn()}
      />,
    );

    expect(screen.getByTestId("connection-error")).toHaveTextContent("this view was out of date");
  });
});
