import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { rendererBindingFor } from "@/generated/renderer-bindings";
import { describeRefusal } from "@/lib/refusal";
import {
  buildSandboxDocument,
  checkSandboxPayload,
  createSandboxNonce,
  DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES,
  readSandboxMessage,
  SANDBOX_FRAME_SANDBOX,
  type SandboxRefusal,
} from "@/lib/sandbox-frame";
import { buildSubmitGate, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

export type DesignPromptKind = "click-region" | "button" | "text-input";

export interface DesignPrompt {
  id: string;
  kind: DesignPromptKind;
  label?: string;
  selector?: string;
  placeholder?: string;
}

export interface PriorVariant {
  envelope_id: string;
  variant_id: string;
  title?: string;
  caption?: string;
  html: string;
}

export interface DesignIterationEnvelope {
  v: number;
  id: string;
  type: "tangent.design-iteration";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    caption?: string;
    variant_id?: string;
    html?: string;
    prompts?: DesignPrompt[];
    prior_variants?: PriorVariant[];
  };
  meta?: Record<string, unknown>;
}

export interface DesignIterationResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    variant_id: string;
    action_id: string;
    action_kind: DesignPromptKind;
    value: string;
  };
  completedAt?: string;
}

export type DesignIterationProps = {
  envelope: DesignIterationEnvelope;
  onSubmit: (response: DesignIterationResponse) => void;
  onCancel: () => void;
};

const MESSAGE_TYPE = "tangent:design-iteration";

/** The kind whose manifest classifies this renderer. */
const KIND = "tangent.design-iteration";

/**
 * The payload ceiling for this renderer's untrusted markup.
 *
 * Read from the manifest binding rather than chosen here: ADR 0003 §2.6 makes
 * `inline_payload_limit_bytes` the publisher's declaration capped by the host,
 * and a renderer that picked its own number would be a third opinion about a
 * limit that already has two owners. A missing binding falls back to the shared
 * default rather than to no limit.
 */
function payloadLimitBytes(): number {
  const binding = rendererBindingFor(KIND);
  if (binding && binding.inlinePayloadLimitBytes > 0) {
    return binding.inlinePayloadLimitBytes;
  }
  return DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES;
}

type DisplayVariant = {
  envelopeID: string;
  variantID: string;
  title: string;
  caption?: string;
  html: string;
  current: boolean;
};

export function DesignIteration({ envelope, onSubmit, onCancel }: DesignIterationProps) {
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  // One nonce for this mount. It is the fourth of the four checks (origin,
  // source, nonce, schema) and the only one that tells this frame's reply from
  // a replay of a message the same frame sent for an earlier variant.
  const nonceRef = useRef<string>(createSandboxNonce());
  const [activeVariantID, setActiveVariantID] = useState<string>(
    envelope.data?.variant_id ?? envelope.id,
  );
  const [textValues, setTextValues] = useState<Record<string, string>>({});

  const prompts = envelope.data?.prompts ?? [];
  const currentVariantID = envelope.data?.variant_id ?? envelope.id;
  const variants = useMemo(() => {
    const prior = (envelope.data?.prior_variants ?? []).map<DisplayVariant>((variant) => ({
      envelopeID: variant.envelope_id,
      variantID: variant.variant_id,
      title: variant.title ?? variant.variant_id,
      caption: variant.caption,
      html: variant.html,
      current: variant.variant_id === currentVariantID,
    }));
    const hasCurrent = prior.some((variant) => variant.current);
    if (hasCurrent) {
      return prior;
    }
    return [
      ...prior,
      {
        envelopeID: envelope.id,
        variantID: currentVariantID,
        title: envelope.title ?? currentVariantID,
        caption: envelope.data?.caption,
        html: envelope.data?.html ?? "",
        current: true,
      },
    ];
  }, [
    currentVariantID,
    envelope.data?.caption,
    envelope.data?.html,
    envelope.data?.prior_variants,
    envelope.id,
    envelope.title,
  ]);

  useEffect(() => {
    setActiveVariantID(currentVariantID);
  }, [currentVariantID]);

  // The provenance rule lives in lib/sandbox-frame, not here.
  //
  // What this listener used to do was skip its own source comparison whenever
  // either input was missing — `if (iframeWindow && event.source && ...)` — so
  // a message that arrived before the frame mounted, or one carrying a null
  // source, reached `onSubmit` unchecked (CW-20260904-0138). The correction is
  // not a tighter condition in this file; it is that deciding whether a
  // `postMessage` is real is not a component's decision. Every listener in the
  // SPA that reads a sandboxed frame goes through `readSandboxMessage`, which
  // refuses on absence at every step.
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const activation = readSandboxMessage(event, {
        frameWindow: iframeRef.current?.contentWindow,
        nonce: nonceRef.current,
        messageType: MESSAGE_TYPE,
      });
      if (!activation) {
        return;
      }
      onSubmit({
        v: 1,
        envelopeId: envelope.id,
        kind: "data",
        status: "submitted",
        payload: {
          variant_id: activation.variantId || currentVariantID,
          action_id: activation.actionId,
          action_kind: activation.actionKind,
          value: activation.value,
        },
        completedAt: new Date().toISOString(),
      });
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [currentVariantID, envelope.id, onSubmit]);

  const activeVariant = variants.find((variant) => variant.variantID === activeVariantID) ??
    variants[variants.length - 1] ?? {
      envelopeID: envelope.id,
      variantID: currentVariantID,
      title: envelope.title ?? currentVariantID,
      html: envelope.data?.html ?? "",
      caption: envelope.data?.caption,
      current: true,
    };

  // The click regions the shim binds inside the frame. Only `click-region`
  // prompts cross the sandbox boundary; buttons and text inputs are the
  // parent's own controls and are never reachable from untrusted markup.
  const regions = useMemo(
    () =>
      prompts
        .filter((prompt) => prompt.kind === "click-region" && prompt.selector)
        .map((prompt) => ({
          id: prompt.id,
          selector: prompt.selector ?? "",
          label: prompt.label,
        })),
    [prompts],
  );
  const refusal: SandboxRefusal | null = checkSandboxPayload(
    activeVariant.html,
    payloadLimitBytes(),
  );
  const srcDoc = refusal
    ? ""
    : buildSandboxDocument({
        html: activeVariant.html,
        regions,
        variantId: activeVariant.variantID,
        messageType: MESSAGE_TYPE,
        nonce: nonceRef.current,
        parentOrigin: window.location.origin,
      });
  const buttonPrompts = prompts.filter((prompt) => prompt.kind === "button");
  const textPrompts = prompts.filter((prompt) => prompt.kind === "text-input");

  const revealRequirement = useRevealRequirement();

  const submitAction = (actionID: string, actionKind: DesignPromptKind, value: string) => {
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        variant_id: currentVariantID,
        action_id: actionID,
        action_kind: actionKind,
        value,
      },
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="design-iteration-root" className="w-full max-w-6xl">
      <CardHeader className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="text-lg">{envelope.title ?? "Design iteration"}</CardTitle>
            {envelope.data?.caption ? (
              <p className="text-sm text-zinc-400">{envelope.data.caption}</p>
            ) : null}
            {envelope.context ? (
              <p className="text-sm whitespace-pre-wrap text-zinc-500">{envelope.context}</p>
            ) : null}
          </div>
          <div className="rounded-full border border-zinc-700 bg-zinc-900 px-3 py-1 text-xs text-zinc-300">
            variant {currentVariantID}
          </div>
        </div>

        <div
          className="flex flex-wrap gap-2"
          data-testid="design-iteration-tabs"
          role="tablist"
          aria-label="Design variants"
        >
          {variants.map((variant, index) => (
            <button
              key={`${variant.envelopeID}-${variant.variantID}`}
              type="button"
              id={`design-iteration-tab-${variant.variantID}`}
              data-testid={`design-iteration-tab-${variant.variantID}`}
              role="tab"
              aria-selected={activeVariantID === variant.variantID}
              aria-controls="design-iteration-variant-panel"
              onClick={() => setActiveVariantID(variant.variantID)}
              className={cn(
                "rounded-full border px-3 py-1 text-xs transition-colors",
                activeVariantID === variant.variantID
                  ? "border-zinc-300 bg-zinc-100 text-zinc-900"
                  : "border-zinc-700 bg-zinc-900 text-zinc-300 hover:border-zinc-500",
              )}
            >
              v{index + 1}
              {variant.current ? " current" : ""}
            </button>
          ))}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        <div
          className="overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950"
          id="design-iteration-variant-panel"
          role="tabpanel"
          aria-labelledby={`design-iteration-tab-${activeVariant.variantID}`}
        >
          <div className="border-b border-zinc-800 bg-zinc-900/80 px-4 py-2 text-xs text-zinc-400">
            {activeVariant.title}
            {activeVariant.caption ? ` • ${activeVariant.caption}` : ""}
          </div>
          {refusal ? (
            // The Refused surface from docs/room-validation-affordances.md, not
            // a second error pattern: red, `role="alert"`, and the fixed
            // "Not submitted: <reason>. <what to do>." shape. A preview that is
            // over the limit is a fact about the world that no amount of
            // clicking fixes, which is exactly what that surface is for.
            <p
              data-testid="design-iteration-sandbox-refusal"
              role="alert"
              className="px-4 py-6 text-sm text-red-300"
            >
              {describeRefusal(refusal.reason, refusal.remedy)}
            </p>
          ) : (
            <iframe
              ref={iframeRef}
              title={`Design iteration ${activeVariant.variantID}`}
              sandbox={SANDBOX_FRAME_SANDBOX}
              srcDoc={srcDoc}
              data-testid="design-iteration-iframe"
              className="h-[540px] w-full bg-white"
            />
          )}
        </div>

        {regions.length > 0 && !refusal ? (
          // Screen-reader and keyboard operators reach the regions inside the
          // frame — the shim gives each one `role="button"`, an `aria-label`,
          // a tab stop, and Enter/Space activation. This line is how they learn
          // the frame is interactive at all before entering it; an iframe with
          // a bare title reads as a document, not as a set of controls.
          <p className="text-xs text-zinc-500" data-testid="design-iteration-region-hint">
            {regions.length === 1
              ? "The preview contains 1 selectable region. Enter the frame and press Enter or Space to choose it."
              : `The preview contains ${regions.length} selectable regions. Enter the frame and press Enter or Space to choose one.`}
          </p>
        ) : null}

        {buttonPrompts.length > 0 ? (
          <div className="flex flex-wrap gap-2" data-testid="design-iteration-buttons">
            {buttonPrompts.map((prompt) => (
              <Button
                key={prompt.id}
                type="button"
                variant="secondary"
                onClick={() => submitAction(prompt.id, "button", prompt.label ?? prompt.id)}
                data-testid={`design-iteration-button-${prompt.id}`}
              >
                {prompt.label ?? prompt.id}
              </Button>
            ))}
          </div>
        ) : null}

        {textPrompts.length > 0 ? (
          <div className="grid gap-3 md:grid-cols-2" data-testid="design-iteration-text-inputs">
            {textPrompts.map((prompt) => {
              // This workflow has no single terminal CTA: each prompt's Send is
              // its own submit path and fires on its own. So each one carries
              // its own gate, adjacent to its own button, rather than a shared
              // footer notice that could not say which prompt it meant.
              const controlID = `design-input-${prompt.id}`;
              const promptLabel = prompt.label ?? prompt.id;
              const value = textValues[prompt.id] ?? "";
              const missing = value.trim().length === 0;
              const gate = buildSubmitGate([
                missing && {
                  controlID,
                  label: promptLabel,
                  message: `"${promptLabel}" still needs an answer.`,
                },
              ]);
              return (
                <div
                  key={prompt.id}
                  className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/60 p-3"
                >
                  {/*
                    The label is envelope-supplied and falls back to the raw
                    machine id, so nothing in it can be relied on to say the
                    field is required. The marker does.
                  */}
                  <label htmlFor={controlID} className="text-sm font-medium text-zinc-100">
                    {promptLabel}
                    <RequiredMark testID={`design-iteration-required-${prompt.id}`} />
                  </label>
                  <div className="flex flex-wrap gap-2">
                    <Input
                      id={controlID}
                      value={value}
                      placeholder={prompt.placeholder}
                      onChange={(event) =>
                        setTextValues((prev) => ({
                          ...prev,
                          [prompt.id]: event.currentTarget.value,
                        }))
                      }
                      aria-required="true"
                      aria-invalid={missing}
                      aria-describedby={`${controlID}-hint`}
                      data-testid={`design-iteration-input-${prompt.id}`}
                    />
                    <Button
                      type="button"
                      onClick={() =>
                        submitAction(prompt.id, "text-input", textValues[prompt.id] ?? "")
                      }
                      disabled={missing}
                      aria-describedby={
                        gate.blocked ? `design-iteration-submit-gate-${prompt.id}` : undefined
                      }
                      data-testid={`design-iteration-submit-${prompt.id}`}
                    >
                      Send
                    </Button>
                  </div>
                  <SubmitGateNotice
                    gate={gate}
                    testID={`design-iteration-submit-gate-${prompt.id}`}
                    action="Send"
                    onReveal={revealRequirement}
                  />
                  <FieldMessage id={`${controlID}-hint`}>
                    Required before Send. Sending answers this prompt on its own and completes the
                    iteration.
                  </FieldMessage>
                </div>
              );
            })}
          </div>
        ) : null}
      </CardContent>

      <CardFooter className="justify-end">
        <Button
          type="button"
          variant="ghost"
          onClick={onCancel}
          data-testid="design-iteration-cancel"
        >
          Cancel
        </Button>
      </CardFooter>
    </Card>
  );
}
