// Triage renders a `tangent.triage` envelope: a list of items (strings
// or objects) with three actions per item — accept, backlog, delete —
// and a single submit action that emits a structured `data` Response.
//
// Per-room state lives entirely inside this component. The owning Room
// component is the boundary: it instantiates a Triage per envelope and
// drops it on cancel/submit. Multi-tab support falls out for free
// because each tab opens its own room and mounts its own Triage.
//
// The triage type is registered via go-envelopes' plugin extension API
// (see internal/envelope/extensions/triage.go); it is NOT in the
// upstream codegen output, so the data shape is hand-defined here.
// When triage upstreams to go-envelopes core (v0.3 plan), this type
// alias collapses into the generated TriageData and the registry
// re-keys to the bare "triage" name.

import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

/**
 * The three v0.1 triage actions. Mirrors fast-triage's vocabulary minus
 * `note` (deferred to v0.2 along with the feedback workflow).
 */
export type TriageAction = "accept" | "backlog" | "delete";

/**
 * Loose shape of a triage item as it arrives from go-envelopes. The
 * registered JSON Schema permits string-or-object; we normalize via
 * normalizeItems() before rendering so the component itself works
 * against a single shape.
 */
export type RawTriageItem = string | Record<string, unknown>;

/**
 * Hand-defined triage envelope shape. Mirrors the manifest in
 * internal/envelope/extensions/triage.go. Mostly a permissive bag —
 * data validation is the server's job; we render whatever arrives.
 */
export interface TriageEnvelope {
  v: number;
  id: string;
  type: "tangent.triage";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    prompt?: string;
    items?: RawTriageItem[];
    context?: Record<string, unknown>;
  };
  meta?: Record<string, unknown>;
}

/**
 * Decision row submitted in the Response payload. itemId is either the
 * incoming item's `id` field (for object-shaped items) or a synthesized
 * "item-N" key (for string items or objects without an id).
 */
export interface TriageDecision {
  itemId: string;
  action: TriageAction;
}

/**
 * Wire-shape of the response we hand back through the WS bridge. The
 * envelopeId is set by the owning Room (it knows which envelope is
 * pending); Triage only owns the kind=data, status=submitted, and
 * payload.decisions slot.
 */
export interface TriageResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    decisions: TriageDecision[];
  };
  completedAt?: string;
}

export type TriageProps = {
  envelope: TriageEnvelope;
  onSubmit: (response: TriageResponse) => void;
  onCancel: () => void;
};

/**
 * Internal item shape after normalization. Carries the synthesized id,
 * a human-readable label for the row header, and an optional context
 * blob rendered as a <pre> when the original item was an object.
 */
type NormalizedItem = {
  itemId: string;
  label: string;
  details?: string;
  /** True when the item was an object literal — drives the <pre> render. */
  isObject: boolean;
};

const ACTIONS: ReadonlyArray<{ id: TriageAction; label: string; cls: string }> = [
  {
    id: "accept",
    label: "Accept",
    cls: "bg-emerald-700 text-emerald-50 hover:bg-emerald-600",
  },
  {
    id: "backlog",
    label: "Backlog",
    cls: "bg-amber-700 text-amber-50 hover:bg-amber-600",
  },
  {
    id: "delete",
    label: "Delete",
    cls: "bg-red-700 text-red-50 hover:bg-red-600",
  },
];

/**
 * Coerce permissive `RawTriageItem` values into renderable rows.
 * Strings → label-only. Objects → label from one of (title, label,
 * summary, name) plus a JSON-stringified detail blob for context.
 */
function normalizeItems(items: RawTriageItem[] | undefined): NormalizedItem[] {
  if (!items || items.length === 0) return [];
  return items.map((item, index) => {
    if (typeof item === "string") {
      return {
        itemId: `item-${index}`,
        label: item,
        isObject: false,
      };
    }
    if (item === null || typeof item !== "object") {
      return {
        itemId: `item-${index}`,
        label: String(item),
        isObject: false,
      };
    }
    const rec = item as Record<string, unknown>;
    const id = typeof rec.id === "string" && rec.id.length > 0 ? rec.id : `item-${index}`;
    const label = pickLabel(rec) ?? `Item ${index + 1}`;
    return {
      itemId: id,
      label,
      details: JSON.stringify(rec, null, 2),
      isObject: true,
    };
  });
}

function pickLabel(rec: Record<string, unknown>): string | null {
  for (const key of ["title", "label", "summary", "name"]) {
    const v = rec[key];
    if (typeof v === "string" && v.length > 0) return v;
  }
  return null;
}

export function Triage({ envelope, onSubmit, onCancel }: TriageProps) {
  const items = useMemo(() => normalizeItems(envelope.data?.items), [envelope.data?.items]);
  const [decisions, setDecisions] = useState<Record<string, TriageAction>>({});

  const undecidedCount = items.length - Object.keys(decisions).length;
  const allDecided = items.length > 0 && undecidedCount === 0;

  const setDecision = (itemId: string, action: TriageAction) => {
    setDecisions((prev) => ({ ...prev, [itemId]: action }));
  };

  const handleSubmit = () => {
    if (!allDecided) return;
    const payload: TriageResponse = {
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        decisions: items.map((item) => ({
          itemId: item.itemId,
          action: decisions[item.itemId] as TriageAction,
        })),
      },
      completedAt: new Date().toISOString(),
    };
    onSubmit(payload);
  };

  const headerTitle = envelope.title ?? "Triage";
  const prompt = envelope.data?.prompt;

  return (
    <Card data-testid="triage-root" className="w-full max-w-2xl">
      <CardHeader>
        <CardTitle className="text-lg">{headerTitle}</CardTitle>
        {prompt ? <p className="text-sm text-zinc-400">{prompt}</p> : null}
        <p className="text-xs text-zinc-500">
          {items.length === 0
            ? "No items to triage."
            : `${items.length} item${items.length === 1 ? "" : "s"} • ${
                undecidedCount === 0 ? "all decided" : `${undecidedCount} undecided`
              }`}
        </p>
      </CardHeader>

      <CardContent className="space-y-3">
        {items.length === 0 ? (
          <p className="text-sm text-zinc-400">
            The envelope contained no items. You can still submit (empty payload) or cancel.
          </p>
        ) : (
          items.map((item) => {
            const current = decisions[item.itemId];
            return (
              <div
                key={item.itemId}
                data-testid={`triage-item-${item.itemId}`}
                className={cn(
                  "rounded-md border border-zinc-800 bg-zinc-950 p-3 space-y-2 transition-colors",
                  current ? "border-zinc-600" : null,
                )}
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-zinc-100 break-words">{item.label}</p>
                    {item.details && item.isObject ? (
                      <pre className="mt-1 max-h-40 overflow-auto rounded border border-zinc-800 bg-zinc-900 p-2 text-[10px] text-zinc-400">
                        {item.details}
                      </pre>
                    ) : null}
                  </div>
                  {current ? (
                    <span
                      data-testid={`triage-item-${item.itemId}-decision`}
                      className="shrink-0 rounded bg-zinc-800 px-2 py-0.5 text-[10px] uppercase tracking-wide text-zinc-300"
                    >
                      {current}
                    </span>
                  ) : null}
                </div>

                <div className="flex flex-wrap gap-2">
                  {ACTIONS.map((action) => {
                    const selected = current === action.id;
                    return (
                      <Button
                        key={action.id}
                        type="button"
                        size="sm"
                        variant={selected ? "default" : "outline"}
                        data-testid={`triage-action-${item.itemId}-${action.id}`}
                        aria-pressed={selected}
                        className={cn("min-w-[5rem]", selected ? action.cls : null)}
                        onClick={() => setDecision(item.itemId, action.id)}
                      >
                        {action.label}
                      </Button>
                    );
                  })}
                </div>
              </div>
            );
          })
        )}
      </CardContent>

      <CardFooter className="flex flex-wrap items-center justify-end gap-2 border-t border-zinc-800">
        <Button type="button" variant="ghost" data-testid="triage-cancel" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="button"
          data-testid="triage-submit"
          disabled={!allDecided}
          onClick={handleSubmit}
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
