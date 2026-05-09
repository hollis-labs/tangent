import type * as React from "react";

import { cn } from "@/lib/utils";

function Label({ className, ...props }: React.ComponentProps<"span">) {
  return <span className={cn("text-sm font-medium text-zinc-100", className)} {...props} />;
}

export { Label };
