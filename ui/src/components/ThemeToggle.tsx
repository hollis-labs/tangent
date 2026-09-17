import { Monitor, Moon, Sun } from "lucide-react";

import { useThemePreference } from "@/hooks/useThemePreference";
import type { ThemePreference } from "@/lib/theme";
import { cn } from "@/lib/utils";

const CYCLE: ThemePreference[] = ["system", "light", "dark"];

const ICON: Record<ThemePreference, typeof Sun> = {
  system: Monitor,
  light: Sun,
  dark: Moon,
};

const LABEL: Record<ThemePreference, string> = {
  system: "Following system appearance",
  light: "Light appearance",
  dark: "Dark appearance",
};

/**
 * A quick-toggle icon, not the settings page — cycles system → light → dark
 * so switching appearance never requires a trip through Settings, matching
 * the pattern most apps put next to their gear icon.
 */
export function ThemeToggle({ className }: { className?: string }) {
  const { preference, setPreference } = useThemePreference();
  const Icon = ICON[preference];

  const cycle = () => {
    const next = CYCLE[(CYCLE.indexOf(preference) + 1) % CYCLE.length];
    setPreference(next);
  };

  return (
    <button
      type="button"
      onClick={cycle}
      title={LABEL[preference]}
      aria-label={`Appearance: ${LABEL[preference]}. Click to change.`}
      className={cn(
        "shrink-0 rounded p-1.5 text-fg-muted outline-none hover:bg-surface hover:text-fg focus-visible:ring-2 focus-visible:ring-primary",
        className,
      )}
    >
      <Icon className="size-4" aria-hidden="true" />
    </button>
  );
}
