import { Button } from "@hollis-labs/design-components";
import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";

// How long the confirmation (or failure) icon stays before resetting.
const FEEDBACK_MS = 2000;

type Outcome = "idle" | "copied" | "failed";

/**
 * Copies an item's durable object id (the `item_id` the docs/inbox MCP tools
 * return and accept) so the operator can hand it to an agent.
 */
export function CopyObjectIdButton({ objectId }: { objectId: string }) {
  const [outcome, setOutcome] = useState<Outcome>("idle");

  useEffect(() => {
    if (outcome === "idle") return;
    const handle = window.setTimeout(() => setOutcome("idle"), FEEDBACK_MS);
    return () => window.clearTimeout(handle);
  }, [outcome]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(objectId);
      setOutcome("copied");
    } catch {
      setOutcome("failed");
    }
  };

  const label =
    outcome === "copied"
      ? "Object id copied"
      : outcome === "failed"
        ? "Copy failed"
        : "Copy object id";
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => void copy()}
      aria-label={label}
      title={label}
      className="text-zinc-400 hover:text-zinc-100"
    >
      {outcome === "copied" ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      <span role="status" className="sr-only">
        {outcome === "idle" ? "" : label}
      </span>
    </Button>
  );
}
