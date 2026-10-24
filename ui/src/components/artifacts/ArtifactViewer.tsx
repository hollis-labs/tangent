import { useEffect, useState } from "react";
import { Markdown } from "@/components/markdown";
import { type ArtifactType, artifactContentURL, fetchArtifactText } from "@/lib/artifacts-api";

/** How an artifact's current revision is shown. One entry per type; a new type is one entry. */
export interface ViewerProps {
  itemID: string;
  revision: number;
  title: string;
  /** Pixel width the preview is held to, or 0 for the full width. */
  width: number;
  /** Bumped by the Reload button to remount the frame. */
  reloadKey: number;
}

/** Sandbox without allow-same-origin: the frame gets an opaque origin (ADR 0015 §6). */
export const ARTIFACT_FRAME_SANDBOX = "allow-scripts allow-popups allow-popups-to-escape-sandbox";

const TEXT_TYPES: ArtifactType[] = [
  "markdown",
  "excalidraw",
  "flow",
  "mermaid",
  "d2",
  "vega-lite",
  "tldraw",
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

function useText(itemID: string, revision: number) {
  const [state, setState] = useState<{ text?: string; error?: string }>({});
  useEffect(() => {
    let live = true;
    setState({});
    fetchArtifactText(itemID, revision)
      .then((text) => live && setState({ text }))
      .catch((reason: Error) => live && setState({ error: reason.message }));
    return () => {
      live = false;
    };
  }, [itemID, revision]);
  return state;
}

function MarkdownView({ itemID, revision }: ViewerProps) {
  const { text, error } = useText(itemID, revision);
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
  const { text, error } = useText(itemID, revision);
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

const VIEWERS: Partial<Record<ArtifactType, (props: ViewerProps) => React.ReactNode>> = {
  html: (p) => <Frame {...p} />,
  image: (p) => <ImageView {...p} />,
  markdown: (p) => <MarkdownView {...p} />,
  pdf: (p) => <PDFView {...p} />,
  video: (p) => <VideoView {...p} />,
};

export function ArtifactViewer({ type, ...props }: ViewerProps & { type: ArtifactType }) {
  const render = VIEWERS[type];
  return <>{render ? render(props) : <SourceView {...props} />}</>;
}
