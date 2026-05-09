import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
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

export function Feedback({ envelope, onSubmit, onCancel }: FeedbackProps) {
  const questions = useMemo(
    () => normalizeQuestions(envelope.data?.questions),
    [envelope.data?.questions],
  );
  const [answers, setAnswers] = useState<AnswersState>(() =>
    Object.fromEntries(questions.map((question) => [question.id, defaultValue(question)])),
  );
  const [notes, setNotes] = useState<NotesState>({});

  const requiredRemaining = questions.filter(
    (question) => !isFilled(question, answers[question.id]),
  ).length;
  const submitDisabled = questions.length === 0 || requiredRemaining > 0;

  const setAnswer = (questionId: string, value: unknown) => {
    setAnswers((prev) => ({ ...prev, [questionId]: value }));
  };

  const toggleMulti = (questionId: string, option: string, checked: boolean) => {
    setAnswers((prev) => {
      const current = arrayValue(prev[questionId]);
      const next = checked ? [...current, option] : current.filter((value) => value !== option);
      return { ...prev, [questionId]: next };
    });
  };

  const handleSubmit = () => {
    if (submitDisabled) {
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
          questions.map((question) => (
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
                <label htmlFor={question.id} className="text-sm font-medium text-zinc-100">
                  {question.label}
                  {question.required ? <span className="ml-1 text-red-300">*</span> : null}
                </label>
                {question.help ? <p className="text-xs text-zinc-400">{question.help}</p> : null}
                {question.suggestion?.rationale ? (
                  <p className="text-xs text-zinc-500">{question.suggestion.rationale}</p>
                ) : null}
              </div>
              <QuestionControl
                question={question}
                value={answers[question.id] ?? defaultValue(question)}
                onChange={setAnswer}
                onToggleMulti={toggleMulti}
              />
              {question.allowNote ? (
                <div className="space-y-1">
                  <label
                    htmlFor={`${question.id}-note`}
                    className="text-xs font-medium text-zinc-400"
                  >
                    Note
                  </label>
                  <Textarea
                    id={`${question.id}-note`}
                    value={notes[question.id] ?? ""}
                    onChange={(event) =>
                      setNotes((prev) => ({ ...prev, [question.id]: event.currentTarget.value }))
                    }
                    placeholder="Optional note"
                    data-testid={`feedback-note-${question.id}`}
                    className="min-h-20"
                  />
                </div>
              ) : null}
            </section>
          ))
        )}
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <Button type="button" variant="ghost" onClick={onCancel} data-testid="feedback-cancel">
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
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
  onChange: (questionId: string, value: unknown) => void;
  onToggleMulti: (questionId: string, option: string, checked: boolean) => void;
};

function QuestionControl({ question, value, onChange, onToggleMulti }: QuestionControlProps) {
  switch (question.type) {
    case "text":
      return (
        <Input
          id={question.id}
          value={stringValue(value)}
          onChange={(event) => onChange(question.id, event.currentTarget.value)}
          placeholder={question.placeholder}
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
          data-testid={`feedback-textarea-${question.id}`}
        />
      );
    case "radio":
    case "select":
      return (
        <RadioGroup data-testid={`feedback-radio-${question.id}`}>
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
        <div className="space-y-2" data-testid={`feedback-checkbox-${question.id}`}>
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
        </div>
      );
    }
  }
}
