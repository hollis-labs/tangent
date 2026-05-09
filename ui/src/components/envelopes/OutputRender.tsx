import { useState } from "react";

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

export function OutputRender({ envelope, onSubmit, onCancel }: OutputRenderProps) {
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const output = envelope.data;
  const markdown = output?.markdown ?? "";
  const filename = output?.filename?.trim() || "tangent-output.md";

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(markdown);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  };

  const handleDownload = () => {
    const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
    const url = window.URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.URL.revokeObjectURL(url);
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
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
          {output?.summary ? <p className="text-sm text-zinc-300">{output.summary}</p> : null}
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
          {copyState === "copied" ? (
            <span className="text-xs text-emerald-400" data-testid="output-render-copy-status">
              Copied
            </span>
          ) : null}
          {copyState === "failed" ? (
            <span className="text-xs text-red-400" data-testid="output-render-copy-status">
              Copy failed
            </span>
          ) : null}
        </div>

        <section className="space-y-2" data-testid="output-render-markdown">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Markdown</p>
          <pre className="whitespace-pre-wrap break-words rounded-lg border border-zinc-800 bg-zinc-950 p-4 text-sm leading-6 text-zinc-100">
            {markdown}
          </pre>
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
