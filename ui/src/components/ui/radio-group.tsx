import type * as React from "react";

import { cn } from "@/lib/utils";

function RadioGroup({ className, ...props }: React.ComponentProps<"div">) {
  return <div role="radiogroup" className={cn("space-y-2", className)} {...props} />;
}

type RadioGroupItemProps = Omit<React.ComponentProps<"input">, "type">;

function RadioGroupItem({ className, ...props }: RadioGroupItemProps) {
  return (
    <input
      type="radio"
      className={cn(
        "h-4 w-4 border-zinc-600 bg-zinc-950 text-zinc-100 accent-zinc-100 focus:ring-1 focus:ring-zinc-500 disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { RadioGroup, RadioGroupItem };
