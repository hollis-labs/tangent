import { useEffect, useState } from "react";

type Mode = "light" | "dark";

/**
 * Light or dark as the person sees it: the page's own background, not an attribute that could
 * disagree with the stylesheet. Re-read whenever the document's theme attributes change.
 */
function readMode(): Mode {
  const probe = getComputedStyle(document.body).backgroundColor;
  const match = probe.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  if (!match) return "light";
  const [r, g, b] = [Number(match[1]), Number(match[2]), Number(match[3])];
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255 < 0.5 ? "dark" : "light";
}

export function useThemeMode(): Mode {
  const [mode, setMode] = useState<Mode>(readMode);
  useEffect(() => {
    const update = () => setMode(readMode());
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, { attributes: true });
    observer.observe(document.body, { attributes: true });
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", update);
    return () => {
      observer.disconnect();
      media.removeEventListener("change", update);
    };
  }, []);
  return mode;
}
