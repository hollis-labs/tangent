import { useEffect, useState } from "react";

import {
  applyThemePreference,
  loadThemePreference,
  saveThemePreference,
  type ThemePreference,
} from "@/lib/theme";

/**
 * Applies the persisted (or system-default) theme preference on mount, and
 * re-applies it whenever the OS preference changes while "system" is active.
 * Tangent has one instance of this in the tree (TabStrip, always mounted),
 * so there is one listener and one source of truth for the DOM attributes
 * design-tokens' setMode writes — not a store to synchronize.
 */
export function useThemePreference() {
  const [preference, setPreference] = useState<ThemePreference>(() => loadThemePreference());

  useEffect(() => {
    applyThemePreference(preference);
    saveThemePreference(preference);
    if (preference !== "system" || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => applyThemePreference("system");
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [preference]);

  return { preference, setPreference };
}
