import { useState } from "react";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import { buildSubmitGate, describedBy, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

import type { CurrentDraft } from "./BlockDraft";

export interface ProseRevisionSuggestion {
  id: string;
  label?: string;
  original_text?: string;
  suggested_text: string;
  reason?: string;
}

export interface ProseRevisionEnvelope {
  v: number;
  id: string;
  type: "tangent.prose-revision";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    lens: "review" | "copy" | "style";
    revision_id?: string;
    block_id?: string;
    label?: string;
    summary?: string;
    source_text?: string;
    suggestions?: ProseRevisionSuggestion[];
    current_draft?: CurrentDraft;
  };
  meta?: Record<string, unknown>;
}

export interface ProseRevisionResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    lens: "review" | "copy" | "style";
    revision_id?: string;
    block_id?: string;
    outcomes: Array<{
      suggestion_id: string;
      decision: "accept" | "reject" | "comment";
      comment?: string;
    }>;
    general_comment?: string;
  };
  completedAt: string;
}

export type ProseRevisionProps = {
  envelope: ProseRevisionEnvelope;
  onSubmit: (response: ProseRevisionResponse) => void;
  onCancel: () => void;
};

const DECISIONS = [
  { id: "accept", label: "Accept" },
  { id: "reject", label: "Reject" },
  { id: "comment", label: "Comment" },
] as const;

const LENS_STYLES = {
  review: {
    badge: "Review lens",
    title: "Substance pass",
    accent: "border-sky-700/70 bg-sky-950/40 text-sky-100",
  },
  copy: {
    badge: "Copy lens",
    title: "Copy edit",
    accent: "border-emerald-700/70 bg-emerald-950/40 text-emerald-100",
  },
  style: {
    badge: "Style lens",
    title: "Style pass",
    accent: "border-amber-700/70 bg-amber-950/40 text-amber-100",
  },
} as const;

export function ProseRevision({ envelope, onSubmit, onCancel }: ProseRevisionProps) {
  const lens = envelope.data?.lens ?? "review";
  const lensStyle = LENS_STYLES[lens];
  const suggestions = envelope.data?.suggestions ?? [];
  const currentDraft = envelope.data?.current_draft;
  const [decisions, setDecisions] = useState<Record<string, "accept" | "reject" | "comment">>({});
  const [comments, setComments] = useState<Record<string, string>>({});
  const [generalComment, setGeneralComment] = useState("");

  const revealRequirement = useRevealRequirement();

  const suggestionLabel = (suggestion: ProseRevisionSuggestion, index: number) =>
    suggestion.label ?? `Suggestion ${index + 1}`;
  const commentMissing = (suggestion: ProseRevisionSuggestion) =>
    decisions[suggestion.id] === "comment" && (comments[suggestion.id] ?? "").trim().length === 0;

  const undecided = suggestions.filter((suggestion) => !decisions[suggestion.id]);
  const unresolved = undecided.length;
  const awaitingComment = suggestions.filter(commentMissing);

  // The gate walks the suggestions in the order they are rendered and reports
  // whichever thing each one still owes. Both causes were previously folded
  // into one `submitDisabled` boolean, and the second had no indicator at all:
  // with every suggestion decided but one Comment body left blank, the header
  // said "All suggestions decided" while Submit stayed dead and nothing on
  // screen named the suggestion holding it.
  const gate = buildSubmitGate(
    suggestions.map((suggestion, index) => {
      const label = suggestionLabel(suggestion, index);
      if (!decisions[suggestion.id]) {
        return {
          // Single column: every suggestion is mounted, so there is nothing
          // to reveal — the decision buttons are already in the DOM.
          controlID: `prose-revision-decision-${suggestion.id}-accept`,
          label: `"${label}"`,
          message: `"${label}" still needs a decision.`,
        };
      }
      if (commentMissing(suggestion)) {
        return {
          controlID: `prose-revision-comment-${suggestion.id}`,
          label: `the comment on "${label}"`,
          message: `"${label}" is marked Comment and still needs the comment itself.`,
        };
      }
      return null;
    }),
  );
  const submitDisabled = gate.blocked;

  const handleDecision = (suggestionID: string, decision: "accept" | "reject" | "comment") => {
    setDecisions((current) => ({ ...current, [suggestionID]: decision }));
  };

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
        lens,
        revision_id: envelope.data?.revision_id,
        block_id: envelope.data?.block_id,
        outcomes: suggestions.map((suggestion) => ({
          suggestion_id: suggestion.id,
          decision: decisions[suggestion.id],
          comment: comments[suggestion.id]?.trim() || undefined,
        })),
        general_comment: generalComment.trim() || undefined,
      },
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="prose-revision-root" className="w-full max-w-5xl">
      <CardHeader className="space-y-3">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className={cn(
                "rounded-full border px-3 py-1 text-xs font-medium uppercase tracking-wide",
                lensStyle.accent,
              )}
              data-testid="prose-revision-lens"
            >
              {lensStyle.badge}
            </span>
            <span className="text-xs text-zinc-500">{lensStyle.title}</span>
          </div>
          <CardTitle className="text-lg">{envelope.title ?? "Prose revision"}</CardTitle>
          {envelope.context ? (
            <Markdown content={envelope.context} className="text-zinc-400" />
          ) : null}
          {envelope.data?.summary ? (
            <Markdown content={envelope.data.summary} className="text-zinc-300" />
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          {envelope.data?.revision_id ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              revision {envelope.data.revision_id}
            </span>
          ) : null}
          {envelope.data?.block_id ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              block {envelope.data.block_id}
            </span>
          ) : null}
          {envelope.data?.label ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              {envelope.data.label}
            </span>
          ) : null}
        </div>
      </CardHeader>

      <CardContent className="space-y-5">
        {currentDraft?.markdown ? (
          <section
            className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-4"
            data-testid="prose-revision-current-draft"
          >
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
              Current accepted draft
            </p>
            <Markdown content={currentDraft.markdown} className="mt-3 text-zinc-100" />
          </section>
        ) : null}

        <section className="space-y-2" data-testid="prose-revision-source">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Source text</p>
          <pre className="whitespace-pre-wrap break-words rounded-lg border border-zinc-800 bg-zinc-950 p-4 text-sm leading-6 text-zinc-100">
            {envelope.data?.source_text ?? ""}
          </pre>
        </section>

        <section className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Suggestions</p>
            {/*
              "All suggestions decided" used to be printed the moment the last
              decision landed, even when a Comment decision still owed its
              comment — the reviewer read a green light beside a dead button.
              The counter now reports both outstanding kinds.
            */}
            <p data-testid="prose-revision-counts" className="text-xs text-zinc-500">
              {unresolved === 0 && awaitingComment.length === 0
                ? "All suggestions decided"
                : [
                    unresolved > 0 ? `${unresolved} undecided` : null,
                    awaitingComment.length > 0
                      ? `${awaitingComment.length} awaiting a comment`
                      : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
            </p>
          </div>

          {(suggestions ?? []).map((suggestion, index) => {
            const decision = decisions[suggestion.id];
            return (
              <article
                key={suggestion.id}
                className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/60 p-4"
                data-testid={`prose-revision-suggestion-${suggestion.id}`}
              >
                <div className="space-y-1">
                  <p
                    id={`prose-revision-suggestion-${suggestion.id}-label`}
                    className="text-sm font-medium text-zinc-100"
                  >
                    {suggestionLabel(suggestion, index)}
                  </p>
                  {/*
                    `reason` is the agent's justification for proposing the
                    change — read-only, and not the reviewer's "comment" below.
                    The two words are the workflow's own and stay as they are;
                    the hint on the comment box is what keeps them apart.
                  */}
                  {suggestion.reason ? (
                    <Markdown
                      data-testid={`prose-revision-reason-${suggestion.id}`}
                      content={suggestion.reason}
                      className="text-zinc-400"
                    />
                  ) : null}
                </div>

                {suggestion.original_text ? (
                  <div className="space-y-1">
                    <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500">
                      Current wording
                    </p>
                    <p
                      data-testid={`prose-revision-original-${suggestion.id}`}
                      className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-300"
                    >
                      {suggestion.original_text}
                    </p>
                  </div>
                ) : null}

                <div className="space-y-1">
                  <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500">
                    Suggested change
                  </p>
                  <p
                    data-testid={`prose-revision-suggested-${suggestion.id}`}
                    className="whitespace-pre-wrap rounded-md border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100"
                  >
                    {suggestion.suggested_text}
                  </p>
                </div>

                <div className="space-y-2">
                  <div className="flex items-center">
                    <span
                      id={`prose-revision-decision-label-${suggestion.id}`}
                      className="text-[11px] font-medium uppercase tracking-wide text-zinc-500"
                    >
                      Decision
                    </span>
                    <RequiredMark
                      active={!decision}
                      testID={`prose-revision-decision-required-${suggestion.id}`}
                    />
                  </div>
                  <fieldset
                    aria-labelledby={`prose-revision-suggestion-${suggestion.id}-label prose-revision-decision-label-${suggestion.id}`}
                    className="flex flex-wrap gap-2"
                  >
                    {DECISIONS.map((option) => (
                      <button
                        type="button"
                        key={option.id}
                        id={`prose-revision-decision-${suggestion.id}-${option.id}`}
                        onClick={() => handleDecision(suggestion.id, option.id)}
                        data-testid={`prose-revision-decision-${suggestion.id}-${option.id}`}
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
                </div>

                <div className="space-y-2">
                  <label
                    htmlFor={`prose-revision-comment-${suggestion.id}`}
                    className="block text-sm font-medium text-zinc-100"
                  >
                    {decision === "comment" ? "Required comment" : "Optional note"}
                    <RequiredMark
                      active={decision === "comment"}
                      testID={`prose-revision-comment-required-${suggestion.id}`}
                    />
                  </label>
                  <Textarea
                    id={`prose-revision-comment-${suggestion.id}`}
                    value={comments[suggestion.id] ?? ""}
                    onChange={(event) => {
                      const nextValue = event.currentTarget.value;
                      setComments((current) => ({
                        ...current,
                        [suggestion.id]: nextValue,
                      }));
                    }}
                    className="min-h-24"
                    aria-required={decision === "comment"}
                    aria-invalid={commentMissing(suggestion)}
                    aria-describedby={describedBy(
                      `prose-revision-comment-hint-${suggestion.id}`,
                      commentMissing(suggestion) && `prose-revision-comment-error-${suggestion.id}`,
                    )}
                    data-testid={`prose-revision-comment-${suggestion.id}`}
                    placeholder={
                      decision === "comment"
                        ? "Explain what should change before you accept it."
                        : "Optional note for this suggestion."
                    }
                  />
                  <FieldMessage id={`prose-revision-comment-hint-${suggestion.id}`}>
                    Your reply to this suggestion, separate from the agent's own reason for
                    proposing it. Choosing Comment makes it the recorded answer and Submit waits for
                    it; under any other decision it is an optional note.
                  </FieldMessage>
                  {commentMissing(suggestion) ? (
                    <FieldMessage
                      id={`prose-revision-comment-error-${suggestion.id}`}
                      tone="error"
                      testID={`prose-revision-comment-error-${suggestion.id}`}
                    >
                      This suggestion is marked Comment. Write the comment.
                    </FieldMessage>
                  ) : null}
                </div>
              </article>
            );
          })}
        </section>

        <section className="space-y-2">
          {/*
            Typographically identical to the per-suggestion comment above it,
            and one of them can be mandatory. The badge and the hint are what
            tell them apart.
          */}
          <label
            htmlFor="prose-revision-general-comment"
            className="block text-sm font-medium text-zinc-100"
          >
            Overall note
            <span
              aria-hidden="true"
              data-testid="prose-revision-general-comment-optional"
              className="ml-2 rounded bg-zinc-700/40 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-[0.12em] text-zinc-400"
            >
              optional
            </span>
          </label>
          <Textarea
            id="prose-revision-general-comment"
            value={generalComment}
            onChange={(event) => {
              setGeneralComment(event.currentTarget.value);
            }}
            className="min-h-24"
            aria-describedby="prose-revision-general-comment-hint"
            data-testid="prose-revision-general-comment"
            placeholder="Optional note about the pass as a whole."
          />
          <FieldMessage id="prose-revision-general-comment-hint">
            Optional. Covers the pass as a whole and never stands in for a comment a single
            suggestion is waiting on.
          </FieldMessage>
        </section>
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        <SubmitGateNotice
          gate={gate}
          testID="prose-revision-submit-gate"
          action="Submit"
          onReveal={revealRequirement}
        />
        <Button
          type="button"
          variant="ghost"
          onClick={onCancel}
          data-testid="prose-revision-cancel"
        >
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          aria-describedby={gate.blocked ? "prose-revision-submit-gate" : undefined}
          data-testid="prose-revision-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
