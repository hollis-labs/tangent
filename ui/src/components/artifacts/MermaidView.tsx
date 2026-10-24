import { useEffect, useId, useState } from "react";
import { useArtifactText } from "./useArtifactText";
import { useThemeMode } from "./useThemeMode";
import type { ViewerProps } from "./viewer-types";
import { ZoomPane } from "./ZoomPane";

/** Renders Mermaid text to SVG. `strict` mode sanitises labels and refuses click handlers and scripts. */
export default function MermaidView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  const mode = useThemeMode();
  const id = `mermaid-${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  const [svg, setSvg] = useState<string | null>(null);
  const [problem, setProblem] = useState("");
  useEffect(() => {
    if (text === undefined) return;
    let live = true;
    setProblem("");
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: mode === "dark" ? "dark" : "default",
        });
        const result = await mermaid.render(`${id}-${revision}`, text);
        if (live) setSvg(result.svg);
      } catch (reason) {
        if (live) setProblem((reason as Error).message || "The diagram could not be drawn.");
      }
    })();
    return () => {
      live = false;
    };
  }, [text, mode, id, revision]);

  if (error)
    return (
      <p role="alert" className="p-4">
        {error}
      </p>
    );
  if (problem)
    return (
      <div className="h-full overflow-auto p-4">
        <p role="alert">The diagram could not be drawn: {problem}</p>
        <pre className="mt-3 whitespace-pre-wrap text-xs">{text}</pre>
      </div>
    );
  if (!svg)
    return (
      <p role="status" className="p-4">
        Loading…
      </p>
    );
  return (
    <ZoomPane>
      {/* biome-ignore lint/security/noDangerouslySetInnerHtml: Mermaid output, produced in securityLevel "strict" which sanitises it */}
      <div data-testid="artifact-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />
    </ZoomPane>
  );
}
