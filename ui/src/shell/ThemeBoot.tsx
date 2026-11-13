import { useEffect } from "react";
import { applyThemePreference, loadThemePreference } from "@/lib/theme";

/**
 * Applies the stored theme preference when the app starts, sign-in screens
 * included, and re-applies it when the OS preference changes. The toggle on the
 * Settings page changes the preference; this reads whatever is stored at the time,
 * so the two never disagree about what "system" should do.
 */
export function ThemeBoot() {
  useEffect(() => {
    applyThemePreference(loadThemePreference());
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => applyThemePreference(loadThemePreference());
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);
  return null;
}
