import { Minus, Plus, RotateCcw } from "lucide-react";
import { type ReactNode, useState } from "react";

const STEPS = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3];

/** A scrollable pane with zoom buttons, for viewers that produce a static picture (SVG). */
export function ZoomPane({ children }: { children: ReactNode }) {
  const [index, setIndex] = useState(3);
  const scale = STEPS[index] ?? 1;
  const button =
    "inline-flex h-8 w-8 items-center justify-center rounded border border-border bg-bg-elevated hover:bg-surface-hover disabled:opacity-40";
  return (
    <div className="relative h-full">
      <div
        className="absolute right-2 top-2 z-10 flex items-center gap-1"
        role="toolbar"
        aria-label="Zoom"
      >
        <button
          type="button"
          className={button}
          aria-label="Zoom out"
          disabled={index === 0}
          onClick={() => setIndex((i) => Math.max(0, i - 1))}
        >
          <Minus size={14} />
        </button>
        <span className="min-w-10 text-center text-xs text-fg-muted">
          {Math.round(scale * 100)}%
        </span>
        <button
          type="button"
          className={button}
          aria-label="Zoom in"
          disabled={index === STEPS.length - 1}
          onClick={() => setIndex((i) => Math.min(STEPS.length - 1, i + 1))}
        >
          <Plus size={14} />
        </button>
        <button
          type="button"
          className={button}
          aria-label="Reset zoom"
          onClick={() => setIndex(3)}
        >
          <RotateCcw size={14} />
        </button>
      </div>
      <div className="h-full overflow-auto p-4">
        <div
          style={{
            transform: `scale(${scale})`,
            transformOrigin: "top left",
            width: "fit-content",
          }}
        >
          {children}
        </div>
      </div>
    </div>
  );
}
