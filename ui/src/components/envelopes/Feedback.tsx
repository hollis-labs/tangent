// Feedback renders a `tangent.feedback` envelope: a list of envelope-authored
// questions, some of which the author marked `required`.
//
// Requiredness here is data, not a mode the operator chose — it is true from
// the moment the envelope mounts. That is why this file marks a question
// invalid only once the operator has actually engaged with it (or has been
// sent to it by the submit gate): painting every required question red before
// anyone has typed a character is noise, not validation. The requirement
// itself is announced up front, through the marker, the hint and
// `aria-required`; the red line is reserved for "you emptied this".

import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import { buildSubmitGate, describedBy, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

export type FeedbackQuestionType =
  | "radio"
  | "checkbox"
  | "select"
  | "multiselect"
  | "text"
  | "textarea";

export interface FeedbackQuestionOption {
  value: string;
  label: string;
  help?: string;
}

export interface FeedbackQuestion {
  id: string;
  type: FeedbackQuestionType;
  label: string;
  help?: string;
  required?: boolean;
  options?: FeedbackQuestionOption[];
  default?: unknown;
  suggestion?: {
    value: unknown;
    rationale?: string;
  };
  allowNote?: boolean;
  placeholder?: string;
}

export interface FeedbackEnvelope {
  v: number;
  id: string;
  type: "tangent.feedback";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    prompt?: string;
    layout?: "inline" | "walkthrough" | "auto";
    questions?: FeedbackQuestion[];
  };
  meta?: Record<string, unknown>;
}

export interface FeedbackAnswer {
  questionId: string;
  value: unknown;
  acceptedSuggestion?: boolean;
  note?: string;
}

export interface FeedbackResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    answers: FeedbackAnswer[];
  };
  completedAt?: string;
}

export type FeedbackProps = {
  envelope: FeedbackEnvelope;
  onSubmit: (response: FeedbackResponse) => void;
  onCancel: () => void;
};

type AnswersState = Record<string, unknown>;
type NotesState = Record<string, string>;

function normalizeQuestions(questions: FeedbackQuestion[] | undefined): FeedbackQuestion[] {
  if (!questions) {
    return [];
  }
  return questions.filter((question) =>
    ["radio", "checkbox", "select", "multiselect", "text", "textarea"].includes(question.type),
  );
}

function defaultValue(question: FeedbackQuestion): unknown {
  if (question.default !== undefined) {
    return question.default;
  }
  if (question.type === "checkbox" || question.type === "multiselect") {
    return [];
  }
  return "";
}

function isFilled(question: FeedbackQuestion, value: unknown): boolean {
  if (!question.required) {
    return true;
  }
  if (question.type === "checkbox" || question.type === "multiselect") {
    return Array.isArray(value) && value.length > 0;
  }
  return typeof value === "string"
    ? value.trim().length > 0
    : value !== undefined && value !== null;
}

function arrayValue(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function valuesEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

/**
 * True for the question types rendered as a set of inputs rather than one.
 *
 * These are the types whose `<label htmlFor={question.id}>` used to dangle:
 * nothing ever rendered an element carrying the bare question id, so the
 * label pointed at nothing and the question text was never announced with
 * the control. They are labelled as a group instead.
 */
function isGroupQuestion(question: FeedbackQuestion): boolean {
  return (
    question.type === "radio" ||
    question.type === "select" ||
    question.type === "checkbox" ||
    question.type === "multiselect"
  );
}

/**
 * The DOM id the submit gate should focus for a question.
 *
 * For a single control that is the control's own id; for a group it is the
 * first option's input, which is both focusable and the target of its own
 * `<label htmlFor>`. A group with no options has nothing to focus.
 */
function focusTargetID(question: FeedbackQuestion): string {
  if (!isGroupQuestion(question)) {
    return question.id;
  }
  const first = (question.options ?? [])[0];
  return first ? `${question.id}-${first.value}` : "";
}

export function Feedback({ envelope, onSubmit, onCancel }: FeedbackProps) {
  const questions = useMemo(
    () => normalizeQuestions(envelope.data?.questions),
    [envelope.data?.questions],
  );
  const [answers, setAnswers] = useState<AnswersState>(() =>
    Object.fromEntries(questions.map((question) => [question.id, defaultValue(question)])),
  );
  const [notes, setNotes] = useState<NotesState>({});
  // Questions the operator has touched. See the note at the top of the file:
  // envelope-declared requiredness is not an error until someone has had a go
  // at the question.
  const [touched, setTouched] = useState<ReadonlySet<string>>(() => new Set());

  const revealRequirement = useRevealRequirement();

  const unanswered = questions.filter((question) => !isFilled(question, answers[question.id]));
  const requiredRemaining = unanswered.length;

  const markTouched = (questionId: string) => {
    setTouched((prev) => (prev.has(questionId) ? prev : new Set(prev).add(questionId)));
  };

  // One requirement per outstanding question, in the order they are rendered,
  // so the notice names the first by the question author's own label and
  // counts the rest. A bare red asterisk said "required" but never said
  // *which* question was still holding Submit down.
  const gate = buildSubmitGate([
    questions.length === 0 && {
      controlID: "",
      label: "questions",
      message: "this feedback envelope has no answerable questions.",
    },
    ...unanswered.map((question) => ({
      controlID: focusTargetID(question),
      label: question.label,
      message: `"${question.label}" is required and still needs an answer.`,
      // Sending the operator to a question is also the moment its own error
      // line becomes fair: they have now been shown the field.
      reveal: () => markTouched(question.id),
    })),
  ]);
  const submitDisabled = gate.blocked;

  const setAnswer = (questionId: string, value: unknown) => {
    markTouched(questionId);
    setAnswers((prev) => ({ ...prev, [questionId]: value }));
  };

  const toggleMulti = (questionId: string, option: string, checked: boolean) => {
    markTouched(questionId);
    setAnswers((prev) => {
      const current = arrayValue(prev[questionId]);
      const next = checked ? [...current, option] : current.filter((value) => value !== option);
      return { ...prev, [questionId]: next };
    });
  };

  const handleSubmit = () => {
    if (gate.blocked) {
      revealRequirement(gate.first);
      return;
    }
    const payload: FeedbackResponse = {
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        answers: questions.map((question) => {
          const value = answers[question.id] ?? defaultValue(question);
          return {
            questionId: question.id,
            value,
            acceptedSuggestion: question.suggestion
              ? valuesEqual(value, question.suggestion.value)
              : undefined,
            note: notes[question.id] || undefined,
          };
        }),
      },
      completedAt: new Date().toISOString(),
    };
    onSubmit(payload);
  };

  return (
    <Card data-testid="feedback-root" className="w-full max-w-2xl">
      <CardHeader>
        <CardTitle className="text-lg">{envelope.title ?? "Feedback"}</CardTitle>
        {envelope.data?.prompt ? (
          <p className="text-sm text-zinc-400">{envelope.data.prompt}</p>
        ) : null}
        {envelope.context ? (
          <p className="text-sm text-zinc-500 whitespace-pre-wrap">{envelope.context}</p>
        ) : null}
        <p className="text-xs text-zinc-500">
          {questions.length} question{questions.length === 1 ? "" : "s"}
          {requiredRemaining > 0
            ? ` • ${requiredRemaining} required remaining`
            : " • ready to submit"}
        </p>
      </CardHeader>

      <CardContent className="space-y-4">
        {questions.length === 0 ? (
          <p className="text-sm text-zinc-400">
            No supported questions were present in the feedback envelope.
          </p>
        ) : (
          questions.map((question) => {
            const required = Boolean(question.required);
            const invalid = !isFilled(question, answers[question.id]);
            const showError = invalid && touched.has(question.id);
            const labelID = `${question.id}-label`;
            const hintID = `${question.id}-hint`;
            const errorID = `${question.id}-error`;
            const description = describedBy(hintID, showError && errorID);
            // Radio and checkbox questions render a set of inputs, so the
            // question text labels the group; only single controls can carry
            // an `htmlFor`.
            const grouped = isGroupQuestion(question);
            return (
              <section
                key={question.id}
                data-testid={`feedback-question-${question.id}`}
                className={cn(
                  "space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/60 p-4",
                  question.required && !isFilled(question, answers[question.id])
                    ? "border-zinc-700"
                    : null,
                )}
              >
                <div className="space-y-1">
                  {grouped ? (
                    <span id={labelID} className="block text-sm font-medium text-zinc-100">
                      {question.label}
                      <RequiredMark active={required} testID={`feedback-required-${question.id}`} />
                    </span>
                  ) : (
                    <label
                      id={labelID}
                      htmlFor={question.id}
                      className="block text-sm font-medium text-zinc-100"
                    >
                      {question.label}
                      <RequiredMark active={required} testID={`feedback-required-${question.id}`} />
                    </label>
                  )}
                  {/*
                    Always rendered, because it is what `aria-describedby`
                    points at — and because "required" was previously stated
                    only by a red asterisk with no legend anywhere on the card.
                  */}
                  <FieldMessage id={hintID} testID={`feedback-hint-${question.id}`}>
                    {[question.help, required ? "Required." : "Optional."]
                      .filter(Boolean)
                      .join(" ")}
                  </FieldMessage>
                  {question.suggestion?.rationale ? (
                    <p className="text-xs text-zinc-500">{question.suggestion.rationale}</p>
                  ) : null}
                </div>
                <QuestionControl
                  question={question}
                  value={answers[question.id] ?? defaultValue(question)}
                  required={required}
                  invalid={required && invalid}
                  labelID={labelID}
                  describedByIDs={description}
                  onChange={setAnswer}
                  onToggleMulti={toggleMulti}
                />
                {showError ? (
                  <FieldMessage id={errorID} tone="error" testID={`feedback-error-${question.id}`}>
                    This question is required. Answer it before submitting.
                  </FieldMessage>
                ) : null}
                {question.allowNote ? (
                  <div className="space-y-1">
                    {/*
                      A per-question aside, sitting directly under a control
                      that may itself be a required textarea. "Note" alone
                      was indistinguishable from the question above it, so the
                      label says what it is and the hint says it is never
                      part of the gate.
                    */}
                    <label
                      htmlFor={`${question.id}-note`}
                      className="block text-xs font-medium text-zinc-400"
                    >
                      Optional note
                    </label>
                    <Textarea
                      id={`${question.id}-note`}
                      value={notes[question.id] ?? ""}
                      onChange={(event) => {
                        // Read the value before the updater: React pools the
                        // synthetic event, so a lazy `event.currentTarget`
                        // inside the updater is null by the time it runs and
                        // typing here threw.
                        const nextValue = event.currentTarget.value;
                        setNotes((prev) => ({ ...prev, [question.id]: nextValue }));
                      }}
                      placeholder="Optional note"
                      aria-describedby={`${question.id}-note-hint`}
                      data-testid={`feedback-note-${question.id}`}
                      className="min-h-20"
                    />
                    <FieldMessage id={`${question.id}-note-hint`}>
                      Optional. Kept alongside the answer above and never required to submit.
                    </FieldMessage>
                  </div>
                ) : null}
              </section>
            );
          })
        )}
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        <SubmitGateNotice
          gate={gate}
          testID="feedback-submit-gate"
          action="Submit"
          onReveal={revealRequirement}
        />
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="feedback-cancel">
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          aria-describedby={gate.blocked ? "feedback-submit-gate" : undefined}
          data-testid="feedback-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}

type QuestionControlProps = {
  question: FeedbackQuestion;
  value: unknown;
  required: boolean;
  invalid: boolean;
  /** Id of the element carrying the question text, for the grouped types. */
  labelID: string;
  describedByIDs: string | undefined;
  onChange: (questionId: string, value: unknown) => void;
  onToggleMulti: (questionId: string, option: string, checked: boolean) => void;
};

function QuestionControl({
  question,
  value,
  required,
  invalid,
  labelID,
  describedByIDs,
  onChange,
  onToggleMulti,
}: QuestionControlProps) {
  switch (question.type) {
    case "text":
      return (
        <Input
          id={question.id}
          value={stringValue(value)}
          onChange={(event) => onChange(question.id, event.currentTarget.value)}
          placeholder={question.placeholder}
          aria-required={required}
          aria-invalid={invalid}
          aria-describedby={describedByIDs}
          data-testid={`feedback-input-${question.id}`}
        />
      );
    case "textarea":
      return (
        <Textarea
          id={question.id}
          value={stringValue(value)}
          onChange={(event) => onChange(question.id, event.currentTarget.value)}
          placeholder={question.placeholder}
          aria-required={required}
          aria-invalid={invalid}
          aria-describedby={describedByIDs}
          data-testid={`feedback-textarea-${question.id}`}
        />
      );
    case "radio":
    case "select":
      return (
        <RadioGroup
          aria-labelledby={labelID}
          aria-required={required}
          aria-invalid={invalid}
          aria-describedby={describedByIDs}
          data-testid={`feedback-radio-${question.id}`}
        >
          {(question.options ?? []).map((option) => {
            const inputID = `${question.id}-${option.value}`;
            return (
              <div
                key={option.value}
                className="flex items-start gap-3 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-100"
              >
                <RadioGroupItem
                  id={inputID}
                  name={question.id}
                  checked={stringValue(value) === option.value}
                  onChange={() => onChange(question.id, option.value)}
                  data-testid={`feedback-option-${question.id}-${option.value}`}
                />
                <label htmlFor={inputID} className="cursor-pointer space-y-1">
                  <span className="block">{option.label}</span>
                  {option.help ? (
                    <span className="block text-xs text-zinc-400">{option.help}</span>
                  ) : null}
                </label>
              </div>
            );
          })}
        </RadioGroup>
      );
    case "checkbox":
    case "multiselect": {
      const selected = arrayValue(value);
      return (
        // A checkbox set is a `group`, and `aria-required` is not a supported
        // attribute of that role. The requirement is carried by the marker in
        // the question label and by the hint this group is described by.
        <fieldset
          className="space-y-2"
          aria-labelledby={labelID}
          aria-invalid={invalid}
          aria-describedby={describedByIDs}
          data-testid={`feedback-checkbox-${question.id}`}
        >
          {(question.options ?? []).map((option) => {
            const inputID = `${question.id}-${option.value}`;
            return (
              <div
                key={option.value}
                className="flex items-start gap-3 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-100"
              >
                <Checkbox
                  id={inputID}
                  checked={selected.includes(option.value)}
                  onChange={(event) =>
                    onToggleMulti(question.id, option.value, event.currentTarget.checked)
                  }
                  data-testid={`feedback-option-${question.id}-${option.value}`}
                />
                <label htmlFor={inputID} className="cursor-pointer space-y-1">
                  <span className="block">{option.label}</span>
                  {option.help ? (
                    <span className="block text-xs text-zinc-400">{option.help}</span>
                  ) : null}
                </label>
              </div>
            );
          })}
        </fieldset>
      );
    }
  }
}
