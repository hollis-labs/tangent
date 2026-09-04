// ConnectionStatus renders the Connection lifecycle of one room surface,
// deliberately separate from anything describing the interaction.
//
// ADR 0001 makes them different lifetimes, and the UI has to say so: a tab can
// be connected with nothing to answer, an envelope stays presented while every
// tab goes away, and the reason a submit did nothing is a connection fact
// ("another tab holds the resolver lease"), not an interaction fact. Folding
// those into one "status: ..." line is what made the old view misleading.

import { Button } from "@/components/ui/button";
import { describeRefusal } from "@/lib/refusal";
import { cn } from "@/lib/utils";
import type { ConnectionState, ServerError, SurfaceSync } from "@/lib/ws-client";

export type ConnectionStatusProps = {
  /** Transport status text: connecting, connected, disconnected: <reason>. */
  transport: string;
  connection: ConnectionState | null;
  sync: SurfaceSync | null;
  serverError: ServerError | null;
  onTakeOver: () => void;
  onRelease: () => void;
  onResync: () => void;
};

export function ConnectionStatus({
  transport,
  connection,
  sync,
  serverError,
  onTakeOver,
  onRelease,
  onResync,
}: ConnectionStatusProps) {
  const role = connection?.role ?? null;
  const peers = connection ? connection.connections.filter((entry) => !entry.self) : [];
  const holder = connection?.lease ?? null;
  const holderIsSelf = holder !== null && holder.connection_id === connection?.connectionId;

  return (
    <section
      data-testid="connection-status"
      className="rounded-md border border-zinc-800 bg-zinc-900/60 px-3 py-2 text-xs"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium uppercase tracking-wide text-zinc-500">connection</span>
        <span data-testid="connection-transport" className="text-zinc-300">
          {transport}
        </span>
        {role ? (
          <span
            data-testid="connection-role"
            className={cn(
              "rounded-full px-2 py-0.5 font-medium",
              role === "resolver"
                ? "bg-emerald-500/15 text-emerald-300"
                : "bg-amber-500/15 text-amber-300",
            )}
          >
            {role === "resolver" ? "resolver" : "observer"}
          </span>
        ) : null}
        <span data-testid="connection-peers" className="text-zinc-400">
          {describePeers(peers.length)}
        </span>
        {sync ? (
          <span data-testid="connection-sync" className="text-zinc-500">
            durable rev {sync.surface_revision}
          </span>
        ) : null}
        <div className="ml-auto flex items-center gap-2">
          {role === "observer" ? (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              data-testid="connection-take-over"
              onClick={onTakeOver}
            >
              Take over
            </Button>
          ) : null}
          {role === "resolver" && peers.length > 0 ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              data-testid="connection-release"
              onClick={onRelease}
            >
              Hand off
            </Button>
          ) : null}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            data-testid="connection-resync"
            onClick={onResync}
          >
            Resync
          </Button>
        </div>
      </div>

      {holder && !holderIsSelf ? (
        <p data-testid="connection-lease-holder" className="mt-1 text-amber-300">
          {describeHolder(holder.label ?? holder.connection_id)} is resolving this surface.
        </p>
      ) : null}

      {serverError ? (
        <p data-testid="connection-error" className="mt-1 text-red-300">
          {describeServerError(serverError)}
        </p>
      ) : null}

      {peers.length > 0 ? (
        <ul data-testid="connection-peer-list" className="mt-1 space-y-0.5 text-zinc-500">
          {peers.map((peer) => (
            <li key={peer.connection_id}>
              {peer.label ?? peer.connection_id} — {peer.role}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function describePeers(count: number): string {
  if (count === 0) return "no other clients attached";
  if (count === 1) return "1 other client attached";
  return `${count} other clients attached`;
}

function describeHolder(label: string): string {
  return label.trim() === "" ? "Another client" : label;
}

// The mapping from wire code to Refused copy. The *sentence shape* lives in
// lib/refusal so the renderer-trust and sandbox-payload refusals introduced by
// CW-20260825-0073 — neither of which arrives on this channel — read
// identically without retyping the punctuation.
//
// No case is added here for a renderer trust denial, deliberately. A definition
// this host will not serve is refused before an envelope is ever pushed, and
// its ADR 0003 §8 C7 code reaches the MCP caller rather than this socket. A
// `case` for a code that cannot arrive is untestable dead TypeScript, which is
// this document's own rule for when a case is worth adding.
function describeServerError(error: ServerError): string {
  switch (error.code) {
    case "resolver_lease_held":
      return describeRefusal(
        `${describeHolder(error.lease?.label ?? error.lease?.connection_id ?? "")} holds the resolver lease`,
        "Take over to answer here.",
      );
    case "stale_presentation":
      return describeRefusal(
        "this view was out of date",
        "It has been refreshed — please answer again.",
      );
    case "room_closed":
      return "Not submitted: this room is closed.";
    case "not_authorized":
      // A room URL is a locator, not a credential: this tab reached the room
      // but its browser session may not answer here. Reloading mints a fresh
      // session, which is the whole recovery.
      return describeRefusal(
        "this browser session is not authorized to answer here",
        "Reload Tangent, then try again.",
      );
    default:
      return error.message || `Not submitted: ${error.code}`;
  }
}
