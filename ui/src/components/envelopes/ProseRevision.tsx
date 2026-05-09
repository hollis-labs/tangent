import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
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

  const unresolved = suggestions.filter((suggestion) => !decisions[suggestion.id]).length;
  const submitDisabled =
    unresolved > 0 ||
    suggestions.some(
      (suggestion) =>
        decisions[suggestion.id] === "comment" &&
        (comments[suggestion.id] ?? "").trim().length === 0,
    );

  const handleDecision = (suggestionID: string, decision: "accept" | "reject" | "comment") => {
    setDecisions((current) => ({ ...current, [suggestionID]: decision }));
  };

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
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
          {envelope.data?.summary ? (
            <p className="text-sm text-zinc-300">{envelope.data.summary}</p>
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
            <pre className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-zinc-100">
              {currentDraft.markdown}
            </pre>
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
            <p className="text-xs text-zinc-500">
              {unresolved === 0 ? "All suggestions decided" : `${unresolved} undecided`}
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
                  <p className="text-sm font-medium text-zinc-100">
                    {suggestion.label ?? `Suggestion ${index + 1}`}
                  </p>
                  {suggestion.reason ? (
                    <p className="text-sm text-zinc-400">{suggestion.reason}</p>
                  ) : null}
                </div>

                {suggestion.original_text ? (
                  <div className="space-y-1">
                    <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500">
                      Current wording
                    </p>
                    <p className="whitespace-pre-wrap rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-300">
                      {suggestion.original_text}
                    </p>
                  </div>
                ) : null}

                <div className="space-y-1">
                  <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500">
                    Suggested change
                  </p>
                  <p className="whitespace-pre-wrap rounded-md border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100">
                    {suggestion.suggested_text}
                  </p>
                </div>

                <div className="flex flex-wrap gap-2">
                  {DECISIONS.map((option) => (
                    <button
                      type="button"
                      key={option.id}
                      onClick={() => handleDecision(suggestion.id, option.id)}
                      data-testid={`prose-revision-decision-${suggestion.id}-${option.id}`}
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

                <div className="space-y-2">
                  <label
                    htmlFor={`prose-revision-comment-${suggestion.id}`}
                    className="text-sm font-medium text-zinc-100"
                  >
                    {decision === "comment" ? "Required comment" : "Optional note"}
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
                    data-testid={`prose-revision-comment-${suggestion.id}`}
                    placeholder={
                      decision === "comment"
                        ? "Explain what should change before you accept it."
                        : "Optional note for this suggestion."
                    }
                  />
                </div>
              </article>
            );
          })}
        </section>

        <section className="space-y-2">
          <label
            htmlFor="prose-revision-general-comment"
            className="text-sm font-medium text-zinc-100"
          >
            Overall note
          </label>
          <Textarea
            id="prose-revision-general-comment"
            value={generalComment}
            onChange={(event) => {
              setGeneralComment(event.currentTarget.value);
            }}
            className="min-h-24"
            data-testid="prose-revision-general-comment"
            placeholder="Optional note about the pass as a whole."
          />
        </section>
      </CardContent>

      <CardFooter className="justify-end gap-3">
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
          data-testid="prose-revision-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
