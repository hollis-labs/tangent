import { useEffect, useRef, useState } from "react";
import { useArtifactText } from "./useArtifactText";
import { useThemeMode } from "./useThemeMode";
import type { ViewerProps } from "./viewer-types";

const refuse = () =>
  Promise.reject(new Error("External data is not allowed in an artifact. Put the values inline."));

/** A loader that refuses every URL: a chart carries its own data (inline `values`). */
const closedLoader = { load: refuse, sanitize: refuse, http: refuse, file: refuse };

/**
 * Renders a Vega-Lite chart. Expressions run in vega-interpreter, so no code is evaluated and the
 * page's CSP can stay free of `unsafe-eval`; the loader never reaches the network or Tangent's API.
 */
export default function VegaView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  const mode = useThemeMode();
  const host = useRef<HTMLDivElement>(null);
  const [problem, setProblem] = useState("");
  useEffect(() => {
    if (text === undefined || !host.current) return;
    let live = true;
    let finalize: (() => void) | undefined;
    setProblem("");
    (async () => {
      try {
        const spec = JSON.parse(text);
        const [{ default: embed }, { expressionInterpreter }] = await Promise.all([
          import("vega-embed"),
          import("vega-interpreter"),
        ]);
        if (!live || !host.current) return;
        const result = await embed(host.current, spec, {
          actions: false,
          renderer: "svg",
          ast: true,
          expr: expressionInterpreter,
          loader: closedLoader as never,
          theme: mode === "dark" ? "dark" : undefined,
        });
        finalize = () => result.finalize();
        if (!live) result.finalize();
      } catch (reason) {
        if (live) setProblem((reason as Error).message || "The chart could not be drawn.");
      }
    })();
    return () => {
      live = false;
      finalize?.();
    };
  }, [text, mode]);

  if (error)
    return (
      <p role="alert" className="p-4">
        {error}
      </p>
    );
  return (
    <div className="h-full overflow-auto p-4" data-testid="artifact-vega">
      {problem ? (
        <>
          <p role="alert">The chart could not be drawn: {problem}</p>
          <pre className="mt-3 whitespace-pre-wrap text-xs">{text}</pre>
        </>
      ) : null}
      <div ref={host} />
    </div>
  );
}
