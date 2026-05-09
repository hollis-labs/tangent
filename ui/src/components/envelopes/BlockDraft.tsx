import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

export interface CurrentDraftBlock {
  block_id: string;
  label?: string;
  mode?: "section" | "paragraph";
  content: string;
}

export interface CurrentDraft {
  block_count: number;
  markdown?: string;
  blocks: CurrentDraftBlock[];
}

export interface BlockDraftEnvelope {
  v: number;
  id: string;
  type: "tangent.block-draft";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    block_id?: string;
    mode?: "section" | "paragraph";
    label?: string;
    content?: string;
    rationale?: string;
    outline_hint?: string;
    current_draft?: CurrentDraft;
  };
  meta?: Record<string, unknown>;
}

export interface BlockDraftResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    decision: "accept" | "revise" | "inline_edit" | "redirect";
    block_id?: string;
    mode?: "section" | "paragraph";
    feedback?: string;
    edited_text?: string;
  };
  completedAt: string;
}

export type BlockDraftProps = {
  envelope: BlockDraftEnvelope;
  onSubmit: (response: BlockDraftResponse) => void;
  onCancel: () => void;
};

const DECISIONS = [
  { id: "accept", label: "Accept" },
  { id: "revise", label: "Request revision" },
  { id: "inline_edit", label: "Inline edit" },
  { id: "redirect", label: "Different direction" },
] as const;

export function BlockDraft({ envelope, onSubmit, onCancel }: BlockDraftProps) {
  const [decision, setDecision] = useState<BlockDraftResponse["payload"]["decision"]>("accept");
  const [feedback, setFeedback] = useState("");
  const [editedText, setEditedText] = useState(envelope.data?.content ?? "");

  const mode = envelope.data?.mode ?? "section";
  const currentDraft = envelope.data?.current_draft;
  const feedbackLabel =
    decision === "revise"
      ? "Revision notes"
      : decision === "redirect"
        ? "New direction"
        : decision === "inline_edit"
          ? "Edit notes"
          : "Optional note";
  const feedbackRequired = decision === "revise" || decision === "redirect";
  const editedRequired = decision === "inline_edit";
  const submitDisabled =
    (feedbackRequired && feedback.trim().length === 0) ||
    (editedRequired && editedText.trim().length === 0);

  const handleSubmit = () => {
    if (submitDisabled) {
      return;
    }
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        decision,
        block_id: envelope.data?.block_id,
        mode,
        feedback: feedback.trim() || undefined,
        edited_text: decision === "inline_edit" ? editedText.trim() || undefined : undefined,
      },
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="block-draft-root" className="w-full max-w-4xl">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">{envelope.title ?? "Draft block"}</CardTitle>
          {envelope.context ? (
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          {envelope.data?.block_id ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              block {envelope.data.block_id}
            </span>
          ) : null}
          <span className="rounded-full border border-zinc-700 px-2 py-1">{mode}</span>
          {envelope.data?.label ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              {envelope.data.label}
            </span>
          ) : null}
        </div>
      </CardHeader>

      <CardContent className="space-y-5">
        {currentDraft && currentDraft.blocks.length > 0 ? (
          <section
            className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-4"
            data-testid="block-draft-current-draft"
          >
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
              Current draft so far
            </p>
            <p className="mt-2 text-sm text-zinc-400">
              {currentDraft.block_count} accepted block{currentDraft.block_count === 1 ? "" : "s"}
            </p>
            {currentDraft.markdown ? (
              <pre className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-zinc-100">
                {currentDraft.markdown}
              </pre>
            ) : null}
          </section>
        ) : null}

        <section className="space-y-3" data-testid="block-draft-candidate">
          <div className="space-y-1">
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
              Proposed block
            </p>
            {envelope.data?.outline_hint ? (
              <p className="text-sm text-zinc-400">{envelope.data.outline_hint}</p>
            ) : null}
          </div>
          <pre className="whitespace-pre-wrap break-words rounded-lg border border-zinc-800 bg-zinc-950 p-4 text-sm leading-6 text-zinc-100">
            {envelope.data?.content ?? ""}
          </pre>
          {envelope.data?.rationale ? (
            <p className="text-sm text-zinc-500">{envelope.data.rationale}</p>
          ) : null}
        </section>

        <section className="space-y-3" data-testid="block-draft-actions">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Response</p>
          <div className="flex flex-wrap gap-2">
            {DECISIONS.map((option) => (
              <button
                type="button"
                key={option.id}
                onClick={() => setDecision(option.id)}
                data-testid={`block-draft-decision-${option.id}`}
                className={cn(
                  "rounded-full border px-3 py-2 text-sm transition-colors",
                  decision === option.id
                    ? "border-zinc-200 bg-zinc-100 text-zinc-900"
                    : "border-zinc-700 bg-zinc-950 text-zinc-200 hover:border-zinc-500",
                )}
              >
                {option.label}
              </button>
            ))}
          </div>
        </section>

        {decision === "inline_edit" ? (
          <section className="space-y-2" data-testid="block-draft-inline-edit">
            <label htmlFor="block-draft-edited-text" className="text-sm font-medium text-zinc-100">
              Final block text
            </label>
            <Textarea
              id="block-draft-edited-text"
              value={editedText}
              onChange={(event) => setEditedText(event.currentTarget.value)}
              className="min-h-40"
              data-testid="block-draft-edited-text"
            />
          </section>
        ) : null}

        <section className="space-y-2">
          <label htmlFor="block-draft-feedback" className="text-sm font-medium text-zinc-100">
            {feedbackLabel}
          </label>
          <Textarea
            id="block-draft-feedback"
            value={feedback}
            onChange={(event) => setFeedback(event.currentTarget.value)}
            placeholder={
              feedbackRequired
                ? "Tell the agent what to change."
                : "Optional note for the next pass."
            }
            className="min-h-24"
            data-testid="block-draft-feedback"
          />
        </section>
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="block-draft-cancel">
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          data-testid="block-draft-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
