// EnvelopeRouter is the type → component dispatcher. PR 4 shipped a
// JSON-debug placeholder; PR 5 backs it with a real registry.
//
// Lookup rules:
//   - Pull the envelope's `type` field if present.
//   - Defer to lib/envelope-registry for the component.
//   - Fall back to the JSON debug renderer when the type is missing
//     or unregistered. The fallback ensures unknown envelopes are
//     still inspectable in dev rather than silently swallowed.
//
// The Router itself remains imperative-state-free: callbacks flow
// down from the owning Room, which owns the WSClient.

import { lookup } from "@/lib/envelope-registry";

type Props = {
  envelope: unknown;
  onSubmit: (response: unknown) => void;
  onCancel: () => void;
  roomID?: string;
};

export function EnvelopeRouter({ envelope, onSubmit, onCancel, roomID }: Props) {
  const type = readType(envelope);
  const Component = type ? lookup(type) : null;

  if (Component) {
    return (
      <Component envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} roomID={roomID} />
    );
  }

  // Fallback: unknown / missing type. Render the raw JSON for
  // debuggability AND a Cancel control so the user can release the
  // server-side Push (otherwise the MCP caller waits until the room
  // timeout, which is bad UX for a typo'd envelope type).
  return (
    <div className="space-y-2" data-testid="envelope-router-fallback">
      <p className="text-xs text-zinc-500">
        {type
          ? `No component registered for "${type}". Showing raw payload.`
          : "Envelope has no type. Showing raw payload."}
      </p>
      <pre className="whitespace-pre-wrap break-words rounded border border-zinc-800 bg-zinc-900 p-3 text-xs text-zinc-200">
        {safeJson(envelope)}
      </pre>
      <div className="flex justify-end">
        <button
          type="button"
          onClick={onCancel}
          className="rounded bg-zinc-800 hover:bg-zinc-700 px-3 py-1 text-xs"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

function readType(envelope: unknown): string | null {
  if (!envelope || typeof envelope !== "object") return null;
  const t = (envelope as { type?: unknown }).type;
  return typeof t === "string" && t.length > 0 ? t : null;
}

function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch (err) {
    return `(failed to stringify: ${(err as Error).message})`;
  }
}
