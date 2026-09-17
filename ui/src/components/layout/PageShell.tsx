import type { ElementType, ReactNode } from "react";

import { cn } from "@/lib/utils";

type PageShellProps = {
  /** Element the shell renders as. Callers keep their route's own semantics
   * (`main`, `div`, ...) instead of PageShell dictating one. */
  as?: ElementType;
  children: ReactNode;
  className?: string;
};

/**
 * The chrome every top-level route renders into: token background/text, and
 * exactly the height Layout's flex column (see App.tsx) leaves below
 * TabStrip. No route computes its own `100vh - Npx` — that arithmetic is
 * where the tab-to-tab layout shift came from, since three routes each
 * guessed a different constant and one guessed none at all.
 */
export function PageShell({ as: Tag = "div", children, className }: PageShellProps) {
  return <Tag className={cn("h-full min-h-0 bg-bg text-fg", className)}>{children}</Tag>;
}
