import { ExternalLink } from "lucide-react";
import { useArtifactText } from "./useArtifactText";
import type { ViewerProps } from "./viewer-types";

/** Embeds a Figma file or frame by link. The server only accepts https figma.com URLs. */
export default function FigmaView({ itemID, revision, title }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  if (error)
    return (
      <p role="alert" className="p-4">
        {error}
      </p>
    );
  if (text === undefined)
    return (
      <p role="status" className="p-4">
        Loading…
      </p>
    );
  const link = text.trim();
  const embed = `https://www.figma.com/embed?embed_host=tangent&url=${encodeURIComponent(link)}`;
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-end gap-2 border-b border-border px-3 py-1 text-xs">
        <a
          href={link}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 underline"
        >
          Open in Figma <ExternalLink size={12} />
        </a>
      </div>
      {/* Figma's own origin: allow-same-origin here is about figma.com, not Tangent. */}
      <iframe
        title={`${title} (Figma)`}
        src={embed}
        sandbox="allow-scripts allow-same-origin allow-popups allow-forms"
        allowFullScreen
        className="min-h-0 flex-1 border-0 bg-white"
        data-testid="artifact-figma"
      />
    </div>
  );
}
