import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import { buildSubmitGate, describedBy, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

// The answer control's DOM id, matched by its `<label htmlFor>` and by the
// submit gate so "marked required" and "what the gate focuses" stay one fact.
const ANSWER_ID = "interview-answer";
const OUTPUT_SHAPE_ID = "interview-output-shape";

export interface InterviewQuestionChoice {
  id: string;
  label: string;
  description?: string;
}

export interface InterviewOutputShape {
  label: string;
  help?: string;
  placeholder?: string;
}

export interface InterviewQuestionEnvelope {
  v: number;
  id: string;
  type: "tangent.interview-question";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    prompt?: string;
    prompt_markdown?: string;
    helper_text?: string;
    choices?: InterviewQuestionChoice[];
    thread_id?: string;
    topic_label?: string;
    output_shape?: InterviewOutputShape;
  };
  meta?: Record<string, unknown>;
}

export interface InterviewQuestionResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    answer_text: string;
    selected_choice_id?: string;
    thread_id?: string;
    topic_label?: string;
    output_shape_signal?: string;
  };
  completedAt?: string;
}

export type InterviewQuestionProps = {
  envelope: InterviewQuestionEnvelope;
  onSubmit: (response: InterviewQuestionResponse) => void;
  onCancel: () => void;
};

export function InterviewQuestion({ envelope, onSubmit, onCancel }: InterviewQuestionProps) {
  const [answerText, setAnswerText] = useState("");
  const [selectedChoiceID, setSelectedChoiceID] = useState<string>("");
  const [outputShapeSignal, setOutputShapeSignal] = useState("");
  // The answer is required from the moment the envelope mounts, so an empty
  // box is not yet a mistake. The marker and `aria-required` announce the
  // requirement up front; the red line waits until the interviewee has been
  // in the field (or has been sent to it by the gate) and left it empty.
  const [answerTouched, setAnswerTouched] = useState(false);

  const revealRequirement = useRevealRequirement();

  const prompt = envelope.data?.prompt_markdown ?? envelope.data?.prompt ?? "";
  const helperText = envelope.data?.helper_text;
  const choices = envelope.data?.choices ?? [];
  const outputShape = envelope.data?.output_shape;
  const answerMissing = answerText.trim().length === 0;

  const gate = buildSubmitGate([
    answerMissing && {
      controlID: ANSWER_ID,
      label: "the answer",
      message: "the answer is still empty.",
      reveal: () => setAnswerTouched(true),
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
        answer_text: answerText.trim(),
        selected_choice_id: selectedChoiceID || undefined,
        thread_id: envelope.data?.thread_id,
        topic_label: envelope.data?.topic_label,
        output_shape_signal: outputShapeSignal.trim() || undefined,
      },
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="interview-question-root" className="w-full max-w-3xl">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">{envelope.title ?? "Interview question"}</CardTitle>
          {prompt ? (
            <div
              className="whitespace-pre-wrap text-sm leading-6 text-zinc-100"
              data-testid="interview-question-prompt"
            >
              {prompt}
            </div>
          ) : null}
          {helperText ? <p className="text-sm text-zinc-400">{helperText}</p> : null}
          {envelope.context ? (
            <p className="whitespace-pre-wrap text-sm text-zinc-500">{envelope.context}</p>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          {envelope.data?.thread_id ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              thread {envelope.data.thread_id}
            </span>
          ) : null}
          {envelope.data?.topic_label ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              topic {envelope.data.topic_label}
            </span>
          ) : null}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {choices.length > 0 ? (
          <section className="space-y-2" data-testid="interview-question-choices">
            <p
              id="interview-question-choices-label"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Quick picks
            </p>
            {/*
              One choice made out of several buttons. Selection was carried by
              colour alone, which says nothing to a screen reader and nothing
              at all in a high-contrast theme.
            */}
            <fieldset
              aria-labelledby="interview-question-choices-label"
              className="flex flex-wrap gap-2"
            >
              {choices.map((choice) => (
                <button
                  type="button"
                  key={choice.id}
                  data-testid={`interview-question-choice-${choice.id}`}
                  aria-pressed={selectedChoiceID === choice.id}
                  onClick={() => setSelectedChoiceID(choice.id)}
                  className={cn(
                    "rounded-full border px-3 py-2 text-sm transition-colors",
                    selectedChoiceID === choice.id
                      ? "border-zinc-200 bg-zinc-100 text-zinc-900"
                      : "border-zinc-700 bg-zinc-950 text-zinc-200 hover:border-zinc-500",
                  )}
                >
                  {choice.label}
                </button>
              ))}
            </fieldset>
            {selectedChoiceID ? (
              <p className="text-xs text-zinc-500">Selected: {selectedChoiceID}</p>
            ) : null}
          </section>
        ) : null}

        <section className="space-y-2">
          <label htmlFor={ANSWER_ID} className="block text-sm font-medium text-zinc-100">
            Answer
            <RequiredMark testID="interview-answer-required" />
          </label>
          <Textarea
            id={ANSWER_ID}
            value={answerText}
            onChange={(event) => {
              setAnswerTouched(true);
              setAnswerText(event.currentTarget.value);
            }}
            placeholder="Write a detailed answer"
            className="min-h-40"
            aria-required="true"
            aria-invalid={answerMissing}
            aria-describedby={describedBy(
              "interview-answer-hint",
              answerMissing && answerTouched && "interview-answer-error",
            )}
            data-testid="interview-question-answer"
          />
          <FieldMessage id="interview-answer-hint">
            Required. This is the answer recorded against the thread — a quick pick above narrows
            the topic but does not stand in for it.
          </FieldMessage>
          {answerMissing && answerTouched ? (
            <FieldMessage id="interview-answer-error" tone="error" testID="interview-answer-error">
              Write the answer before submitting.
            </FieldMessage>
          ) : null}
        </section>

        {outputShape ? (
          <section className="space-y-2" data-testid="interview-question-output-shape">
            {/*
              The label here is envelope-authored, so it is left exactly as the
              agent wrote it — and an agent-written label ("Preferred output
              shape") reads every bit as mandatory as the Answer above. The
              badge and the hint are what say otherwise.
            */}
            <label htmlFor={OUTPUT_SHAPE_ID} className="block text-sm font-medium text-zinc-100">
              {outputShape.label}
              <span
                aria-hidden="true"
                data-testid="interview-output-shape-optional"
                className="ml-2 rounded bg-zinc-700/40 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-[0.12em] text-zinc-400"
              >
                optional
              </span>
            </label>
            <Textarea
              id={OUTPUT_SHAPE_ID}
              value={outputShapeSignal}
              onChange={(event) => setOutputShapeSignal(event.currentTarget.value)}
              placeholder={outputShape.placeholder ?? "Optional output-shape preference"}
              className="min-h-24"
              aria-describedby="interview-output-shape-hint"
              data-testid="interview-question-output-shape-input"
            />
            <FieldMessage id="interview-output-shape-hint">
              {outputShape.help
                ? `${outputShape.help} Optional — Submit never waits on it.`
                : "Optional — Submit never waits on it."}
            </FieldMessage>
          </section>
        ) : null}
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        <SubmitGateNotice
          gate={gate}
          testID="interview-question-submit-gate"
          action="Submit"
          onReveal={revealRequirement}
        />
        <Button
          type="button"
          variant="ghost"
          onClick={onCancel}
          data-testid="interview-question-cancel"
        >
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          aria-describedby={gate.blocked ? "interview-question-submit-gate" : undefined}
          data-testid="interview-question-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
