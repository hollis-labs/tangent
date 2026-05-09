import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

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

  const prompt = envelope.data?.prompt_markdown ?? envelope.data?.prompt ?? "";
  const helperText = envelope.data?.helper_text;
  const choices = envelope.data?.choices ?? [];
  const outputShape = envelope.data?.output_shape;
  const submitDisabled = answerText.trim().length === 0;

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
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Quick picks</p>
            <div className="flex flex-wrap gap-2">
              {choices.map((choice) => (
                <button
                  type="button"
                  key={choice.id}
                  data-testid={`interview-question-choice-${choice.id}`}
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
            </div>
            {selectedChoiceID ? (
              <p className="text-xs text-zinc-500">Selected: {selectedChoiceID}</p>
            ) : null}
          </section>
        ) : null}

        <section className="space-y-2">
          <label htmlFor="interview-answer" className="text-sm font-medium text-zinc-100">
            Answer
          </label>
          <Textarea
            id="interview-answer"
            value={answerText}
            onChange={(event) => setAnswerText(event.currentTarget.value)}
            placeholder="Write a detailed answer"
            className="min-h-40"
            data-testid="interview-question-answer"
          />
        </section>

        {outputShape ? (
          <section className="space-y-2" data-testid="interview-question-output-shape">
            <label htmlFor="interview-output-shape" className="text-sm font-medium text-zinc-100">
              {outputShape.label}
            </label>
            {outputShape.help ? <p className="text-xs text-zinc-400">{outputShape.help}</p> : null}
            <Textarea
              id="interview-output-shape"
              value={outputShapeSignal}
              onChange={(event) => setOutputShapeSignal(event.currentTarget.value)}
              placeholder={outputShape.placeholder ?? "Optional output-shape preference"}
              className="min-h-24"
              data-testid="interview-question-output-shape-input"
            />
          </section>
        ) : null}
      </CardContent>

      <CardFooter className="justify-end gap-3">
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
          data-testid="interview-question-submit"
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}
