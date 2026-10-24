import "@excalidraw/excalidraw/index.css";
import { convertToExcalidrawElements, Excalidraw, restoreElements } from "@excalidraw/excalidraw";
import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";
import { useEffect, useMemo, useState } from "react";
import { useArtifactText } from "./useArtifactText";
import { useThemeMode } from "./useThemeMode";
import type { ViewerProps } from "./viewer-types";

const FONTS = ["Excalifont", "Virgil", "Nunito", "Assistant"];

type Scene = {
  elements?: unknown[];
  appState?: Record<string, unknown>;
  files?: Record<string, unknown>;
};

/**
 * Shows an Excalidraw scene read-only, so an agent's diagram can be panned and zoomed. The scene may
 * be a full export or the skeleton form (`{type, x, y, label: {text}}`), which Excalidraw completes.
 */
export default function ExcalidrawView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  const theme = useThemeMode();
  const [fontsReady, setFontsReady] = useState(false);
  // Excalidraw sizes text boxes with whatever font is loaded when it converts a scene. Wait for the
  // hand-drawn faces first (capped, so a missing font never blocks the diagram).
  useEffect(() => {
    let live = true;
    const load = Promise.all(
      FONTS.map((family) => document.fonts.load(`20px "${family}"`).catch(() => [])),
    ).then(() => undefined);
    const cap = new Promise<void>((resolve) => setTimeout(resolve, 2500));
    void Promise.race([load, cap]).then(() => live && setFontsReady(true));
    return () => {
      live = false;
    };
  }, []);
  const initialData = useMemo(() => {
    if (text === undefined || !fontsReady) return null;
    try {
      const scene = JSON.parse(text) as Scene;
      const elements = convertToExcalidrawElements((scene.elements ?? []) as never, {
        regenerateIds: false,
      });
      return {
        elements,
        appState: { ...(scene.appState ?? {}), viewModeEnabled: true },
        files: (scene.files ?? {}) as never,
        scrollToContent: true,
      };
    } catch (reason) {
      return { problem: (reason as Error).message };
    }
  }, [text, fontsReady]);

  // The editor registers its fonts once it mounts, so text it sized before then can be too narrow.
  // Once they have loaded, measure every text element again.
  const remeasure = (api: ExcalidrawImperativeAPI) => {
    void Promise.all(
      FONTS.map((family) => document.fonts.load(`20px "${family}"`).catch(() => [])),
    ).then(() =>
      setTimeout(
        () =>
          api.updateScene({
            elements: restoreElements(api.getSceneElements(), null, { refreshDimensions: true }),
          }),
        200,
      ),
    );
  };

  if (error)
    return (
      <p role="alert" className="p-4">
        {error}
      </p>
    );
  if (!initialData)
    return (
      <p role="status" className="p-4">
        Loading…
      </p>
    );
  if ("problem" in initialData)
    return (
      <p role="alert" className="p-4">
        This scene could not be read: {initialData.problem}
      </p>
    );
  return (
    <div className="h-full" data-testid="artifact-excalidraw">
      <Excalidraw
        key={revision}
        excalidrawAPI={remeasure}
        initialData={initialData}
        viewModeEnabled
        theme={theme}
        UIOptions={{
          canvasActions: {
            loadScene: false,
            saveToActiveFile: false,
            export: false,
            saveAsImage: true,
          },
        }}
      />
    </div>
  );
}
