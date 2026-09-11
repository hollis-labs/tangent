import { useEffect, useState } from "react";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";

export interface FinalOutput {
  title?: string;
  markdown: string;
  filename?: string;
  format?: "markdown";
  summary?: string;
  updated_at?: string;
  word_count?: number;
}

export interface OutputRenderEnvelope {
  v: number;
  id: string;
  type: "tangent.output-render";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: FinalOutput;
  meta?: Record<string, unknown>;
}

export interface OutputRenderResponse {
  v: 1;
  envelopeId: string;
  kind: "ack";
  status: "submitted";
  completedAt: string;
}

export type OutputRenderProps = {
  envelope: OutputRenderEnvelope;
  onSubmit: (response: OutputRenderResponse) => void;
  onCancel: () => void;
};

// How long a copy/download outcome stays on screen. Without this the label was
// never reset, so "Copy failed" from one attempt sat beside a later successful
// download and read as its result.
const TRANSFER_STATUS_TIMEOUT_MS = 4000;

export function OutputRender({ envelope, onSubmit, onCancel }: OutputRenderProps) {
  // This workflow has no terminal gate by design: it is a read-only
  // acknowledgement with no editable controls, so "Done" is always available.
  // The only outcomes it has to report are the copy and download side effects.
  const [transferState, setTransferState] = useState<{
    tone: "ok" | "error";
    message: string;
  } | null>(null);
  const output = envelope.data;
  const markdown = output?.markdown ?? "";
  const filename = output?.filename?.trim() || "tangent-output.md";

  useEffect(() => {
    if (!transferState) {
      return;
    }
    const handle = window.setTimeout(() => setTransferState(null), TRANSFER_STATUS_TIMEOUT_MS);
    return () => window.clearTimeout(handle);
  }, [transferState]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(markdown);
      setTransferState({ tone: "ok", message: "Copied" });
    } catch {
      setTransferState({ tone: "error", message: "Copy failed" });
    }
  };

  const handleDownload = () => {
    try {
      const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      setTransferState({ tone: "ok", message: `Downloaded ${filename}` });
    } catch {
      // A blocked object URL or a sandboxed download used to fail in silence.
      setTransferState({ tone: "error", message: "Download failed" });
    }
  };

  const handleDone = () => {
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "ack",
      status: "submitted",
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="output-render-root" className="w-full max-w-5xl">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">
            {envelope.title ?? output?.title ?? "Final output"}
          </CardTitle>
          {envelope.context ? (
            <Markdown content={envelope.context} className="text-zinc-400" />
          ) : null}
          {output?.summary ? <Markdown content={output.summary} className="text-zinc-300" /> : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            {output?.format ?? "markdown"}
          </span>
          {output?.filename ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">{output.filename}</span>
          ) : null}
          {typeof output?.word_count === "number" ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              {output.word_count} words
            </span>
          ) : null}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            variant="secondary"
            onClick={handleCopy}
            data-testid="output-render-copy"
          >
            Copy markdown
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={handleDownload}
            data-testid="output-render-download"
          >
            Download .md
          </Button>
          <span
            role="status"
            aria-live="polite"
            data-testid="output-render-copy-status"
            className={
              transferState === null
                ? "sr-only"
                : transferState.tone === "ok"
                  ? "text-xs text-emerald-400"
                  : "text-xs text-red-400"
            }
          >
            {transferState?.message ?? ""}
          </span>
        </div>

        <section className="space-y-2" data-testid="output-render-markdown">
          <h3 className="text-xs font-medium uppercase tracking-wide text-zinc-500">Markdown</h3>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950 p-4">
            <Markdown content={markdown} className="text-zinc-100" />
          </div>
        </section>
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="output-render-cancel">
          Cancel
        </Button>
        <Button type="button" onClick={handleDone} data-testid="output-render-submit">
          Done
        </Button>
      </CardFooter>
    </Card>
  );
}
