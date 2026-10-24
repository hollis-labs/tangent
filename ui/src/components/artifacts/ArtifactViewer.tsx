import { lazy, type ReactNode, Suspense } from "react";
import { Markdown } from "@/components/markdown";
import { type ArtifactType, artifactContentURL, artifactRenderedURL } from "@/lib/artifacts-api";
import { useArtifactText } from "./useArtifactText";
import type { ViewerProps } from "./viewer-types";
import { ZoomPane } from "./ZoomPane";

export type { ViewerProps } from "./viewer-types";

/** Sandbox without allow-same-origin: the frame gets an opaque origin (ADR 0015 §6). */
export const ARTIFACT_FRAME_SANDBOX = "allow-scripts allow-popups allow-popups-to-escape-sandbox";

/** Where Excalidraw finds its fonts. They are served by Tangent, never a CDN (see vite.config.ts). */
const EXCALIDRAW_ASSET_PATH = "/excalidraw-assets/";

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string;
  }
}

// The heavy viewers load on first use, so an inbox with none of them costs nothing.
const ExcalidrawView = lazy(() => {
  window.EXCALIDRAW_ASSET_PATH = EXCALIDRAW_ASSET_PATH;
  return import("./ExcalidrawView");
});
const FigmaView = lazy(() => import("./FigmaView"));
const FlowView = lazy(() => import("./FlowView"));
const MermaidView = lazy(() => import("./MermaidView"));
const VegaView = lazy(() => import("./VegaView"));

const TEXT_TYPES: ArtifactType[] = [
  "markdown",
  "excalidraw",
  "flow",
  "mermaid",
  "d2",
  "vega-lite",
  "figma",
];

/** Whether the source of this type can be read as text (Copy, source view). */
export function isTextArtifact(type: ArtifactType): boolean {
  return type === "html" || TEXT_TYPES.includes(type);
}

function Frame({ itemID, revision, title, width, reloadKey }: ViewerProps) {
  return (
    <div className="flex h-full justify-center overflow-auto bg-surface">
      <iframe
        key={`${revision}:${reloadKey}`}
        title={`${title} (revision ${revision})`}
        sandbox={ARTIFACT_FRAME_SANDBOX}
        src={artifactContentURL(itemID, revision)}
        className="h-full border-0 bg-white"
        style={{ width: width ? `${width}px` : "100%", maxWidth: "100%" }}
        data-testid="artifact-html-frame"
      />
    </div>
  );
}

function ImageView({ itemID, revision, title }: ViewerProps) {
  return (
    <div className="flex h-full items-center justify-center overflow-auto bg-surface p-2">
      <img
        src={artifactContentURL(itemID, revision)}
        alt={title}
        className="max-h-full max-w-full object-contain"
        data-testid="artifact-image"
      />
    </div>
  );
}

function MarkdownView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  return (
    <div className="h-full overflow-auto p-4">
      {error ? (
        <p role="alert">{error}</p>
      ) : text === undefined ? (
        <p role="status">Loading…</p>
      ) : (
        <Markdown content={text} />
      )}
    </div>
  );
}

/** Types whose rich preview is not built yet still show their source, never a blank pane. */
function SourceView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  return (
    <div className="h-full overflow-auto p-4">
      <p className="mb-2 text-xs text-fg-muted">
        Showing the source. A rendered preview for this type is not available yet.
      </p>
      {error ? (
        <p role="alert">{error}</p>
      ) : text === undefined ? (
        <p role="status">Loading…</p>
      ) : (
        <pre className="whitespace-pre-wrap break-words text-xs">{text}</pre>
      )}
    </div>
  );
}

/** D2, drawn by the server. An SVG shown as an image cannot run script or load anything. */
function RenderedView({ itemID, revision, title }: ViewerProps) {
  return (
    <ZoomPane fill>
      <img
        src={artifactRenderedURL(itemID, revision)}
        alt={title}
        className="h-auto w-full"
        data-testid="artifact-rendered"
      />
    </ZoomPane>
  );
}

function PDFView({ itemID, revision, title }: ViewerProps) {
  return (
    <iframe
      title={title}
      src={artifactContentURL(itemID, revision)}
      className="h-full w-full border-0 bg-white"
      data-testid="artifact-pdf"
    />
  );
}

function VideoView({ itemID, revision }: ViewerProps) {
  return (
    <div className="flex h-full items-center justify-center bg-black">
      {/* biome-ignore lint/a11y/useMediaCaption: agent-sent video has no caption track */}
      <video
        src={artifactContentURL(itemID, revision)}
        controls
        className="max-h-full max-w-full"
        data-testid="artifact-video"
      />
    </div>
  );
}

const VIEWERS: Partial<Record<ArtifactType, (props: ViewerProps) => ReactNode>> = {
  html: (p) => <Frame {...p} />,
  image: (p) => <ImageView {...p} />,
  markdown: (p) => <MarkdownView {...p} />,
  excalidraw: (p) => <ExcalidrawView {...p} />,
  flow: (p) => <FlowView {...p} />,
  mermaid: (p) => <MermaidView {...p} />,
  "vega-lite": (p) => <VegaView {...p} />,
  figma: (p) => <FigmaView {...p} />,
  d2: (p) => (p.rendered ? <RenderedView {...p} /> : <SourceView {...p} />),
  pdf: (p) => <PDFView {...p} />,
  video: (p) => <VideoView {...p} />,
};

export function ArtifactViewer({ type, ...props }: ViewerProps & { type: ArtifactType }) {
  const render = VIEWERS[type];
  return (
    <Suspense
      fallback={
        <p role="status" className="p-4">
          Loading viewer…
        </p>
      }
    >
      {render ? render(props) : <SourceView {...props} />}
    </Suspense>
  );
}
