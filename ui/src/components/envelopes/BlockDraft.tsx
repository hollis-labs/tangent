import { useState } from "react";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import { buildSubmitGate, describedBy, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

// Control ids, shared by each control's `<label htmlFor>` and by the submit
// gate that focuses it.
const FEEDBACK_ID = "block-draft-feedback";
const EDITED_TEXT_ID = "block-draft-edited-text";

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

  const revealRequirement = useRevealRequirement();

  const mode = envelope.data?.mode ?? "section";
  const currentDraft = envelope.data?.current_draft;
  // Four labels for one box, two of which are hard requirements. The wording
  // is the workflow's own and stays — but "Edit notes" reads exactly as
  // mandatory as "Revision notes" while only one of them is, and only the
  // Accept wording ever said "Optional". The badge below is what actually
  // tracks the requirement.
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
  const feedbackMissing = feedbackRequired && feedback.trim().length === 0;
  const editedMissing = editedRequired && editedText.trim().length === 0;

  const gate = buildSubmitGate([
    feedbackMissing && {
      controlID: FEEDBACK_ID,
      label: feedbackLabel.toLowerCase(),
      message:
        decision === "revise"
          ? "a revision request still needs its revision notes."
          : "a new direction still needs to be described.",
    },
    editedMissing && {
      controlID: EDITED_TEXT_ID,
      label: "final block text",
      message: "an inline edit still needs the final block text.",
    },
  ]);
  const submitDisabled = gate.blocked;

  const handleSubmit = () => {
    if (gate.blocked) {
      revealRequirement(gate.first);
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
            <Markdown content={envelope.context} className="text-zinc-400" />
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
              <Markdown content={currentDraft.markdown} className="mt-3 leading-6 text-zinc-100" />
            ) : null}
          </section>
        ) : null}

        <section className="space-y-3" data-testid="block-draft-candidate">
          <div className="space-y-1">
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
              Proposed block
            </p>
            {envelope.data?.outline_hint ? (
              <Markdown content={envelope.data.outline_hint} className="text-zinc-400" />
            ) : null}
          </div>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950 p-4">
            <Markdown
              content={envelope.data?.content ?? ""}
              data-testid="block-draft-content"
              className="leading-6 text-zinc-100"
            />
          </div>
          {envelope.data?.rationale ? (
            <Markdown content={envelope.data.rationale} className="text-zinc-500" />
          ) : null}
        </section>

        <section className="space-y-3" data-testid="block-draft-actions">
          <p
            id="block-draft-response-label"
            className="text-xs font-medium uppercase tracking-wide text-zinc-500"
          >
            Response
          </p>
          {/*
            One choice, four buttons, and the choice decides which of the two
            fields below becomes mandatory — so selection has to be readable
            without seeing the fill colour.
          */}
          <fieldset aria-labelledby="block-draft-response-label" className="flex flex-wrap gap-2">
            {DECISIONS.map((option) => (
              <button
                type="button"
                key={option.id}
                id={`block-draft-decision-${option.id}`}
                onClick={() => setDecision(option.id)}
                data-testid={`block-draft-decision-${option.id}`}
                aria-pressed={decision === option.id}
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
          </fieldset>
        </section>

        {decision === "inline_edit" ? (
          <section className="space-y-2" data-testid="block-draft-inline-edit">
            <label htmlFor={EDITED_TEXT_ID} className="block text-sm font-medium text-zinc-100">
              Final block text
              <RequiredMark active={editedRequired} testID="block-draft-edited-text-required" />
            </label>
            <Textarea
              id={EDITED_TEXT_ID}
              value={editedText}
              onChange={(event) => setEditedText(event.currentTarget.value)}
              className="min-h-40"
              placeholder="The exact text this block should end up with."
              aria-required={editedRequired}
              aria-invalid={editedMissing}
              aria-describedby={describedBy(
                "block-draft-edited-text-hint",
                editedMissing && "block-draft-edited-text-error",
              )}
              data-testid="block-draft-edited-text"
            />
            <FieldMessage id="block-draft-edited-text-hint">
              Required. This text is submitted as the block itself — the notes below only describe
              it.
            </FieldMessage>
            {editedMissing ? (
              <FieldMessage
                id="block-draft-edited-text-error"
                tone="error"
                testID="block-draft-edited-text-error"
              >
                An inline edit cannot be empty. Write the final block text.
              </FieldMessage>
            ) : null}
          </section>
        ) : null}

        <section className="space-y-2">
          <label htmlFor={FEEDBACK_ID} className="block text-sm font-medium text-zinc-100">
            {feedbackLabel}
            <RequiredMark active={feedbackRequired} testID="block-draft-feedback-required" />
          </label>
          <Textarea
            id={FEEDBACK_ID}
            value={feedback}
            onChange={(event) => setFeedback(event.currentTarget.value)}
            placeholder={
              feedbackRequired
                ? "Tell the agent what to change."
                : "Optional note for the next pass."
            }
            className="min-h-24"
            aria-required={feedbackRequired}
            aria-invalid={feedbackMissing}
            aria-describedby={describedBy(
              "block-draft-feedback-hint",
              feedbackMissing && "block-draft-feedback-error",
            )}
            data-testid="block-draft-feedback"
          />
          <FieldMessage id="block-draft-feedback-hint">
            Required under Request revision and Different direction, where it is the instruction the
            agent works from. Under Accept and Inline edit it is an optional note and Submit never
            waits on it.
          </FieldMessage>
          {feedbackMissing ? (
            <FieldMessage
              id="block-draft-feedback-error"
              tone="error"
              testID="block-draft-feedback-error"
            >
              {decision === "revise"
                ? "Say what should change before requesting a revision."
                : "Describe the new direction before sending the block back."}
            </FieldMessage>
          ) : null}
        </section>
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        <SubmitGateNotice
          gate={gate}
          testID="block-draft-submit-gate"
          action="Submit"
          onReveal={revealRequirement}
        />
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="block-draft-cancel">
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          aria-describedby={gate.blocked ? "block-draft-submit-gate" : undefined}
          data-testid="block-draft-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
