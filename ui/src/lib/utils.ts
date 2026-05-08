import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Compose Tailwind class names with conditional/object support and
 * conflict resolution. Standard shadcn helper — kept here so the
 * `@/lib/utils` import path used by shadcn primitives resolves.
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
