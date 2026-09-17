import { setMode, type ThemeMode } from "@hollis-labs/design-tokens";

/**
 * "system" is a third state on top of design-tokens' own light/dark
 * ThemeMode — Tangent resolves it to a concrete mode before calling setMode,
 * design-tokens itself has no notion of following the OS preference.
 */
export type ThemePreference = "system" | ThemeMode;

const STORAGE_KEY = "tangent.theme-preference";

export function systemPrefersLight(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: light)").matches
  );
}

export function resolveThemeMode(preference: ThemePreference): ThemeMode {
  return preference === "system" ? (systemPrefersLight() ? "light" : "dark") : preference;
}

export function loadThemePreference(): ThemePreference {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") return stored;
  } catch {
    // Private-browsing / blocked storage: fall through to the default.
  }
  return "system";
}

export function saveThemePreference(preference: ThemePreference): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, preference);
  } catch {
    // Best-effort only — a failed write just means the choice doesn't survive reload.
  }
}

export function applyThemePreference(preference: ThemePreference): void {
  setMode(resolveThemeMode(preference));
}
