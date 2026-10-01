import { type RefObject, useEffect, useRef } from "react";

import type { HITLAttentionLabels } from "@/lib/hitl-api";
import { cn } from "@/lib/utils";

export type AttentionComposerIntent = "note" | "reply" | null;

interface AttentionActionsProps {
  labels?: HITLAttentionLabels;
  intent: AttentionComposerIntent;
  value: string;
  error: string;
  disabled: boolean;
  preparing: boolean;
  onIntent: (intent: AttentionComposerIntent) => void;
  onValueChange: (value: string) => void;
  onAcknowledge: () => void;
  onSubmit: () => void;
}

export function AttentionActions({
  labels = {},
  intent,
  value,
  error,
  disabled,
  preparing,
  onIntent,
  onValueChange,
  onAcknowledge,
  onSubmit,
}: AttentionActionsProps) {
  const describedBy = error ? "hitl-attention-composer-error" : "hitl-attention-effect";
  const previousIntent = useRef<Exclude<AttentionComposerIntent, null> | null>(null);
  const noteTrigger = useRef<HTMLButtonElement>(null);
  const replyTrigger = useRef<HTMLButtonElement>(null);
  const composer = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (intent) {
      previousIntent.current = intent;
      composer.current?.focus();
      return;
    }
    if (previousIntent.current === "note") noteTrigger.current?.focus();
    if (previousIntent.current === "reply") replyTrigger.current?.focus();
    previousIntent.current = null;
  }, [intent]);

  return (
    <div>
      <p id="hitl-attention-effect" className="mb-3 max-w-lg text-xs leading-5 text-fg-muted">
        {preparing
          ? "Preparing an exact acknowledgement revision…"
          : "Acknowledgement records receipt only. It does not accept work or perform an external action."}
      </p>
      {intent ? (
        <div className="border-l-2 border-info pl-4">
          <label
            htmlFor="hitl-attention-composer"
            className="block text-xs font-medium text-fg-secondary"
          >
            {intent === "reply" ? "Reply to caller" : "Acknowledgement note"}
          </label>
          <textarea
            id="hitl-attention-composer"
            ref={composer}
            rows={3}
            maxLength={intent === "reply" ? 12000 : 4000}
            value={value}
            onChange={(event) => onValueChange(event.target.value)}
            aria-invalid={Boolean(error)}
            aria-describedby={describedBy}
            className="mt-2 w-full resize-y border border-border bg-bg-elevated px-3 py-2 text-sm leading-6 text-fg outline-none placeholder:text-fg-muted focus:border-info focus:ring-1 focus:ring-info"
            placeholder={
              intent === "reply"
                ? "Write the reply the caller should receive…"
                : "Record context with this acknowledgement…"
            }
          />
          {error ? (
            <p id="hitl-attention-composer-error" className="mt-1 text-xs text-danger">
              {error}
            </p>
          ) : null}
          <div className="mt-3 flex justify-end gap-2">
            <AttentionButton label="Cancel" variant="quiet" onClick={() => onIntent(null)} />
            <AttentionButton
              label={
                intent === "reply"
                  ? labels.reply || "Acknowledge and reply"
                  : labels.acknowledge_with_note || "Acknowledge with note"
              }
              ariaLabel={`Submit ${
                intent === "reply"
                  ? labels.reply || "acknowledgement and reply"
                  : labels.acknowledge_with_note || "acknowledgement with note"
              }`}
              variant="signal"
              disabled={disabled}
              onClick={onSubmit}
            />
          </div>
        </div>
      ) : null}
      <fieldset className={cn("grid grid-cols-2 justify-end gap-2 sm:flex", intent && "mt-4")}>
        <legend className="sr-only">Attention acknowledgement actions</legend>
        <AttentionButton
          label={labels.acknowledge_with_note || "Add note"}
          variant="quiet"
          disabled={disabled}
          buttonRef={noteTrigger}
          controls="hitl-attention-composer"
          expanded={intent === "note"}
          onClick={() => {
            previousIntent.current = "note";
            onIntent("note");
          }}
        />
        <AttentionButton
          label={labels.reply || "Reply"}
          variant="quiet"
          disabled={disabled}
          buttonRef={replyTrigger}
          controls="hitl-attention-composer"
          expanded={intent === "reply"}
          onClick={() => {
            previousIntent.current = "reply";
            onIntent("reply");
          }}
        />
        <AttentionButton
          label={labels.acknowledge || "Acknowledge"}
          variant="signal"
          disabled={disabled}
          onClick={onAcknowledge}
        />
      </fieldset>
    </div>
  );
}

function AttentionButton({
  label,
  variant,
  disabled,
  buttonRef,
  ariaLabel,
  controls,
  expanded,
  onClick,
}: {
  label: string;
  variant: "signal" | "quiet";
  disabled?: boolean;
  buttonRef?: RefObject<HTMLButtonElement | null>;
  ariaLabel?: string;
  controls?: string;
  expanded?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      ref={buttonRef}
      aria-label={ariaLabel}
      aria-controls={controls}
      aria-expanded={controls ? expanded : undefined}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "min-h-10 border px-3 py-2 text-xs font-semibold outline-none transition-colors focus-visible:ring-2 focus-visible:ring-info disabled:cursor-not-allowed disabled:opacity-45",
        variant === "signal" &&
          "border-info bg-info text-info-fg hover:border-info/90 hover:bg-info/90",
        variant === "quiet" &&
          "border-border bg-surface text-fg-secondary hover:border-info hover:text-fg",
      )}
    >
      {label}
    </button>
  );
}
