import { useEffect, useMemo, useState } from "react";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import { Textarea } from "@/components/ui/textarea";
import {
  buildSubmitGate,
  describedBy,
  focusControl,
  useRevealRequirement,
} from "@/lib/submit-gate";
import {
  buildWizardCanonicalSeedKey,
  clearWizardDraft,
  loadWizardDraft,
  saveWizardDraft,
  WIZARD_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/wizard-draft-storage";

// Focus target for a step change. The whole content region swaps while focus
// sits on a footer button, so the heading is where focus has to land.
const STEP_HEADING_ID = "wizard-step-heading";

type WizardFieldOption = {
  value: string;
  label: string;
};

type WizardField = {
  field_id: string;
  label: string;
  kind?: "text" | "textarea" | "select" | "checkbox" | "attachments";
  placeholder?: string;
  required?: boolean;
  options?: WizardFieldOption[];
};

export type WizardStep = {
  step_id: string;
  title: string;
  description?: string;
  kind?: string;
  optional?: boolean;
  fields?: {
    fields?: WizardField[];
    actions?: Array<{ action_id: string; label: string; description?: string }>;
  };
  branches?: Array<{
    branch_id: string;
    label: string;
    description?: string;
    target_step_id: string;
  }>;
  metadata?: Record<string, unknown>;
};

export type WizardProgress = {
  step_id: string;
  status: "pending" | "in_progress" | "completed" | "skipped" | "blocked";
  revision_id?: string;
  response?: Record<string, unknown>;
  summary?: string;
  completed_at?: string;
  updated_at?: string;
};

export type WizardBranchSelection = {
  step_id: string;
  option_id: string;
  selected_at?: string;
  target_step_id?: string;
};

export type WizardSummary = {
  status?: "not_started" | "in_progress" | "completed" | "blocked";
  headline?: string;
  detail?: string;
  completed_step_count?: number;
  total_step_count?: number;
  completed_at?: string;
  last_completed_step_id?: string;
  current_step_id?: string;
};

export interface WizardEnvelope {
  v: number;
  id: string;
  type: "tangent.wizard";
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    wizard_id: string;
    title?: string;
    description?: string;
    current_step_id?: string;
    steps: WizardStep[];
    progress?: WizardProgress[];
    branch_selections?: WizardBranchSelection[];
    summary?: WizardSummary;
    updated_at?: string;
  };
  meta?: Record<string, unknown>;
}

export interface WizardResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "partial" | "submitted";
  payload: {
    wizard_id: string;
    title?: string;
    description?: string;
    current_step_id: string;
    steps: WizardStep[];
    progress: WizardProgress[];
    branch_selections: WizardBranchSelection[];
    summary: WizardSummary;
    updated_at: string;
  };
  completedAt?: string;
}

type Props = {
  envelope: WizardEnvelope;
  onSubmit: (response: WizardResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function Wizard({ envelope, onSubmit, onCancel, roomID }: Props) {
  const wizardID = envelope.data?.wizard_id ?? "wizard";
  const steps = envelope.data?.steps ?? [];
  const canonicalCurrentStepID = envelope.data?.current_step_id ?? steps[0]?.step_id ?? "";
  const [currentStepID, setCurrentStepID] = useState(canonicalCurrentStepID);
  const [progress, setProgress] = useState<WizardProgress[]>(envelope.data?.progress ?? []);
  const [branchSelections, setBranchSelections] = useState<WizardBranchSelection[]>(
    envelope.data?.branch_selections ?? [],
  );
  const [message, setMessage] = useState<string | null>(null);

  const seedKey = useMemo(
    () =>
      buildWizardCanonicalSeedKey({
        wizardID,
        stepIDs: steps.map((step) => step.step_id),
        currentStepID: canonicalCurrentStepID,
      }),
    [wizardID, steps, canonicalCurrentStepID],
  );

  useEffect(() => {
    setCurrentStepID(canonicalCurrentStepID);
    setProgress(envelope.data?.progress ?? []);
    setBranchSelections(envelope.data?.branch_selections ?? []);
  }, [canonicalCurrentStepID, envelope.data?.progress, envelope.data?.branch_selections]);

  useEffect(() => {
    if (!roomID || !wizardID) return;
    const recovered = loadWizardDraft(roomID, wizardID);
    if (!recovered || recovered.envelopeId !== envelope.id || recovered.baseSeedKey !== seedKey) {
      return;
    }
    setCurrentStepID(recovered.currentStepID || canonicalCurrentStepID);
    setProgress(recovered.progress as WizardProgress[]);
    setBranchSelections(recovered.branchSelections as WizardBranchSelection[]);
    setMessage("Recovered unsent wizard state from this browser.");
  }, [roomID, wizardID, envelope.id, seedKey, canonicalCurrentStepID]);

  useEffect(() => {
    if (!roomID || !wizardID) return;
    const handle = window.setTimeout(() => {
      saveWizardDraft({
        version: 1,
        roomID,
        wizardID,
        envelopeId: envelope.id,
        baseSeedKey: seedKey,
        currentStepID,
        progress: progress as Array<Record<string, unknown>>,
        branchSelections: branchSelections as Array<Record<string, unknown>>,
        savedAt: new Date().toISOString(),
      });
    }, WIZARD_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(handle);
  }, [roomID, wizardID, envelope.id, seedKey, currentStepID, progress, branchSelections]);

  const currentIndex = Math.max(
    0,
    steps.findIndex((step) => step.step_id === currentStepID),
  );
  const currentStep = steps[currentIndex] ?? null;
  const currentProgress =
    progress.find((item) => item.step_id === currentStep?.step_id) ??
    (currentStep ? { step_id: currentStep.step_id, status: "pending", response: {} } : null);

  const revealRequirement = useRevealRequirement();
  // Bumped whenever the step actually changes; the effect below moves focus
  // once the new step has rendered.
  const [stepFocusToken, setStepFocusToken] = useState(0);

  // `field.required` was declared on the field type and read nowhere: a
  // required field rendered byte-identically to an optional one, and the
  // terminal CTA marked the step "completed" no matter what was left blank.
  // This is that declaration finally enforced, scoped to the fields the
  // operator can see — the current step's. Back, Next Step, Save Progress, the
  // action buttons and every `partial` submit are untouched, because a wizard
  // has to stay navigable while it is incomplete.
  const currentStepFields = currentStep?.fields?.fields ?? [];
  const missingRequired = currentStepFields.filter(
    (field) =>
      field.required && !isWizardFieldFilled(field, currentProgress?.response?.[field.field_id]),
  );
  const isFinalStep = currentIndex >= steps.length - 1;
  const submitLabel = isFinalStep ? "Complete Wizard" : "Submit Step";
  const gate = buildSubmitGate(
    missingRequired.map((field) => ({
      controlID: `wizard-field-${field.field_id}`,
      label: field.label,
      message: `"${field.label}" is required before this step can be submitted.`,
    })),
  );

  useEffect(() => {
    if (stepFocusToken === 0) {
      return;
    }
    focusControl(STEP_HEADING_ID);
  }, [stepFocusToken]);

  const completedCount = progress.filter((item) => item.status === "completed").length;
  const summary: WizardSummary = {
    status: completedCount >= steps.length && steps.length > 0 ? "completed" : "in_progress",
    headline: `${completedCount} of ${steps.length} steps completed`,
    detail: envelope.data?.summary?.detail,
    completed_step_count: completedCount,
    total_step_count: steps.length,
    last_completed_step_id:
      progress.filter((item) => item.status === "completed").at(-1)?.step_id ?? "",
    current_step_id: currentStepID,
    completed_at:
      completedCount >= steps.length && steps.length > 0 ? new Date().toISOString() : undefined,
  };

  const persist = (
    status: "partial" | "submitted",
    nextCurrentStepID: string,
    nextProgress: WizardProgress[],
    nextBranchSelections: WizardBranchSelection[],
  ) => {
    const updatedAt = new Date().toISOString();
    const completedNextCount = nextProgress.filter((item) => item.status === "completed").length;
    const isCompleted = completedNextCount >= steps.length && steps.length > 0;
    const payload: WizardResponse["payload"] = {
      wizard_id: wizardID,
      title: envelope.data?.title,
      description: envelope.data?.description,
      current_step_id: nextCurrentStepID,
      steps,
      progress: nextProgress,
      branch_selections: nextBranchSelections,
      summary: {
        ...summary,
        status: isCompleted ? "completed" : "in_progress",
        completed_step_count: completedNextCount,
        current_step_id: nextCurrentStepID,
        completed_at: status === "submitted" && isCompleted ? updatedAt : undefined,
      },
      updated_at: updatedAt,
    };
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status,
      payload,
      completedAt: payload.updated_at,
    });
    if (roomID && wizardID && status === "submitted" && isCompleted) {
      clearWizardDraft(roomID, wizardID);
    }
  };

  const withPatchedCurrentProgress = (patch: Partial<WizardProgress>) => {
    if (!currentStep) return progress;
    const existing = progress.find((item) => item.step_id === currentStep.step_id);
    const nextItem: WizardProgress = {
      step_id: currentStep.step_id,
      status: existing?.status ?? "in_progress",
      revision_id: existing?.revision_id,
      response: existing?.response ?? {},
      summary: existing?.summary,
      completed_at: existing?.completed_at,
      ...existing,
      updated_at: new Date().toISOString(),
      ...patch,
    };
    const rest = progress.filter((item) => item.step_id !== currentStep.step_id);
    return [...rest, nextItem];
  };

  const setFieldValue = (fieldID: string, value: unknown) => {
    const response = { ...(currentProgress?.response ?? {}), [fieldID]: value };
    setProgress(withPatchedCurrentProgress({ response, status: "in_progress" }));
  };

  const handleActionOutput = (actionID: string) => {
    const response = {
      ...(currentProgress?.response ?? {}),
      action_output: {
        action_id: actionID,
        triggered_at: new Date().toISOString(),
      },
    };
    const nextProgress = withPatchedCurrentProgress({
      response,
      status: "completed",
      summary: `Action ${actionID} selected`,
    });
    setProgress(nextProgress);
    persist("partial", currentStepID, nextProgress, branchSelections);
    setMessage(`Action ${actionID} selected.`);
  };

  const setBranchSelection = (optionID: string, targetStepID: string) => {
    if (!currentStep) return;
    setBranchSelections((prev) => [
      ...prev.filter((item) => item.step_id !== currentStep.step_id),
      {
        step_id: currentStep.step_id,
        option_id: optionID,
        selected_at: new Date().toISOString(),
        target_step_id: targetStepID,
      },
    ]);
  };

  const goToStep = (stepID: string) => {
    setCurrentStepID(stepID);
    setStepFocusToken((token) => token + 1);
    persist("partial", stepID, progress, branchSelections);
  };

  const handleNextStep = () => {
    const selectedBranch = branchSelections.find((item) => item.step_id === currentStep?.step_id);
    const nextStepID =
      selectedBranch?.target_step_id ?? steps[currentIndex + 1]?.step_id ?? currentStepID;
    setCurrentStepID(nextStepID);
    setStepFocusToken((token) => token + 1);
    persist("partial", nextStepID, progress, branchSelections);
    setMessage("Moved to the next step.");
  };

  const handleSaveProgress = () => {
    const nextProgress = withPatchedCurrentProgress({
      status: currentProgress?.status ?? "in_progress",
    });
    setProgress(nextProgress);
    persist("partial", currentStepID, nextProgress, branchSelections);
    setMessage("Progress saved to the room.");
  };

  const handleSubmitStep = () => {
    if (!currentStep) return;
    // The one new refusal in this workflow. Everything below it is unchanged,
    // including the payload this sends.
    if (gate.blocked) {
      revealRequirement(gate.first);
      return;
    }
    const selectedBranch = branchSelections.find((item) => item.step_id === currentStep.step_id);
    const nextStepID =
      selectedBranch?.target_step_id ?? steps[currentIndex + 1]?.step_id ?? currentStep.step_id;
    const nextProgress = withPatchedCurrentProgress({
      status: "completed",
      revision_id: `rev-${Date.now()}`,
      completed_at: new Date().toISOString(),
    });
    setProgress(nextProgress);
    const isFinal = currentIndex >= steps.length - 1 && !selectedBranch?.target_step_id;
    const targetStepID = isFinal ? currentStep.step_id : nextStepID;
    setCurrentStepID(targetStepID);
    if (targetStepID !== currentStepID) {
      setStepFocusToken((token) => token + 1);
    }
    persist(isFinal ? "submitted" : "partial", targetStepID, nextProgress, branchSelections);
    setMessage(isFinal ? "Wizard completed." : "Step submitted.");
  };

  if (!currentStep) {
    return null;
  }

  return (
    <Card
      className="w-full max-w-4xl border-zinc-800 bg-zinc-950 text-zinc-100"
      data-testid="wizard-envelope"
    >
      <CardHeader className="space-y-3">
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <CardTitle>{envelope.data?.title ?? envelope.title ?? "Wizard"}</CardTitle>
            {envelope.data?.description ? (
              <Markdown
                data-testid="wizard-description"
                content={envelope.data.description}
                className="text-zinc-400"
              />
            ) : null}
          </div>
          <div className="text-right text-xs text-zinc-500">
            <div>
              {completedCount} / {steps.length} complete
            </div>
            {envelope.data?.updated_at ? <div>saved {envelope.data.updated_at}</div> : null}
          </div>
        </div>

        <ol className="grid gap-2 md:grid-cols-2">
          {steps.map((step, index) => {
            const stepProgress = progress.find((item) => item.step_id === step.step_id);
            const isActive = step.step_id === currentStepID;
            return (
              <li key={step.step_id}>
                <button
                  type="button"
                  onClick={() => goToStep(step.step_id)}
                  aria-current={isActive ? "step" : undefined}
                  data-testid={`wizard-step-${step.step_id}`}
                  className={`w-full rounded-lg border px-3 py-2 text-left ${
                    isActive ? "border-amber-400 bg-amber-500/10" : "border-zinc-800 bg-zinc-900/70"
                  }`}
                >
                  <div className="text-xs text-zinc-500">Step {index + 1}</div>
                  <div className="font-medium">{step.title}</div>
                  <div className="text-xs text-zinc-400">{stepProgress?.status ?? "pending"}</div>
                </button>
              </li>
            );
          })}
        </ol>
      </CardHeader>

      <CardContent className="space-y-5">
        {/*
          Rendered unconditionally so the live region exists before the message
          does — a region that appears together with its text is announced
          unreliably, which is why step changes used to pass in silence.
        */}
        <p
          role="status"
          aria-live="polite"
          data-testid="wizard-message"
          className={message ? "text-sm text-emerald-400" : "sr-only"}
        >
          {message}
        </p>

        <section className="space-y-2">
          <div className="text-xs uppercase tracking-[0.2em] text-zinc-500">
            {currentStep.kind ?? "step"}
          </div>
          <h2 className="text-lg font-semibold" id={STEP_HEADING_ID} tabIndex={-1}>
            {currentStep.title}
          </h2>
          {currentStep.description ? (
            <Markdown
              data-testid="wizard-step-description"
              content={currentStep.description}
              className="text-zinc-400"
            />
          ) : null}
        </section>

        <section className="space-y-4">
          {currentStepFields.map((field) => (
            <FieldControl
              key={field.field_id}
              field={field}
              value={currentProgress?.response?.[field.field_id]}
              missing={missingRequired.some((item) => item.field_id === field.field_id)}
              onChange={(value) => setFieldValue(field.field_id, value)}
            />
          ))}

          {currentStep.fields?.actions && currentStep.fields.actions.length > 0 ? (
            <div className="space-y-2">
              <div className="text-sm font-medium">Actions</div>
              <div className="flex flex-wrap gap-2">
                {currentStep.fields.actions.map((action) => (
                  <Button
                    key={action.action_id}
                    type="button"
                    variant="outline"
                    onClick={() => handleActionOutput(action.action_id)}
                  >
                    {action.label}
                  </Button>
                ))}
              </div>
            </div>
          ) : null}

          {currentStep.branches && currentStep.branches.length > 0 ? (
            /*
              The radios were a bare `<div>` heading over a list of inputs whose
              `aria-label` overrode the visible text, so the description — the
              only thing that says where a branch leads — was never announced.
              A fieldset gives the group a name, and naming and describing by id
              keeps both halves of each option.
            */
            <fieldset className="space-y-2" data-testid="wizard-branches">
              <legend className="text-sm font-medium">Choose the next branch</legend>
              {currentStep.branches.map((branch) => {
                const selected = branchSelections.find(
                  (item) => item.step_id === currentStep.step_id,
                )?.option_id;
                const branchID = `wizard-branch-${currentStep.step_id}-${branch.branch_id}`;
                return (
                  <label
                    key={branch.branch_id}
                    htmlFor={branchID}
                    className="flex items-start gap-3 rounded border border-zinc-800 p-3 text-sm"
                  >
                    <input
                      id={branchID}
                      type="radio"
                      name={`branch-${currentStep.step_id}`}
                      aria-labelledby={`${branchID}-label`}
                      aria-describedby={`${branchID}-description`}
                      checked={selected === branch.branch_id}
                      onChange={() => setBranchSelection(branch.branch_id, branch.target_step_id)}
                      data-testid={branchID}
                    />
                    <div>
                      <div id={`${branchID}-label`} className="font-medium">
                        {branch.label}
                      </div>
                      {/*
                        The id stays on the wrapper because the radio's
                        `aria-describedby` points at it. Only the caller's own
                        description routes through the renderer; the
                        `Next: <step>` fallback is ours.
                      */}
                      <div id={`${branchID}-description`}>
                        {branch.description ? (
                          <Markdown content={branch.description} className="text-zinc-400" />
                        ) : (
                          <span className="text-zinc-400">Next: {branch.target_step_id}</span>
                        )}
                      </div>
                    </div>
                  </label>
                );
              })}
            </fieldset>
          ) : null}
        </section>

        <section className="space-y-2 rounded border border-zinc-800 bg-zinc-900/50 p-4">
          <div className="text-sm font-medium">Review summary</div>
          <div className="text-sm text-zinc-400">{summary.headline}</div>
          <ul className="space-y-1 text-sm text-zinc-300">
            {steps.map((step) => {
              const item = progress.find((entry) => entry.step_id === step.step_id);
              return (
                <li key={step.step_id}>
                  <span className="font-medium">{step.title}</span>:{" "}
                  {item?.summary ?? item?.status ?? "pending"}
                </li>
              );
            })}
          </ul>
        </section>
      </CardContent>

      <CardFooter className="flex flex-wrap justify-between gap-3">
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant="secondary"
            onClick={() => {
              if (currentIndex > 0) {
                goToStep(steps[currentIndex - 1].step_id);
              }
            }}
            disabled={currentIndex === 0}
          >
            Back
          </Button>
          <Button
            type="button"
            variant="secondary"
            onClick={handleNextStep}
            disabled={currentIndex >= steps.length - 1 && !currentStep.branches?.length}
          >
            Next Step
          </Button>
          <Button type="button" variant="outline" onClick={handleSaveProgress}>
            Save Progress
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          {/*
            `mode="attempt"`: the CTA stays live exactly as it always has, and
            validates on click. Disabling it would have been a second behaviour
            change on top of the enforcement itself.
          */}
          <SubmitGateNotice
            gate={gate}
            testID="wizard-submit-gate"
            action={submitLabel}
            mode="attempt"
            onReveal={revealRequirement}
          />
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            type="button"
            data-testid="wizard-submit"
            onClick={handleSubmitStep}
            aria-describedby={gate.blocked ? "wizard-submit-gate" : undefined}
          >
            {submitLabel}
          </Button>
        </div>
      </CardFooter>
    </Card>
  );
}

/**
 * Is a wizard field's stored answer non-empty?
 *
 * Mirrors the shapes `setFieldValue` writes: a checkbox stores a boolean, the
 * attachments editor stores an array of rows, everything else a string.
 */
function isWizardFieldFilled(field: WizardField, value: unknown): boolean {
  switch (field.kind ?? "text") {
    case "checkbox":
      return value === true;
    case "attachments":
      return Array.isArray(value) && value.length > 0;
    default:
      return typeof value === "string"
        ? value.trim().length > 0
        : value !== undefined && value !== null;
  }
}

function FieldControl({
  field,
  value,
  missing,
  onChange,
}: {
  field: WizardField;
  value: unknown;
  /** True while this field is declared required and still empty. */
  missing: boolean;
  onChange: (value: unknown) => void;
}) {
  const kind = field.kind ?? "text";
  const inputID = `wizard-field-${field.field_id}`;
  const required = Boolean(field.required);
  const hintID = `${inputID}-hint`;
  const errorID = `${inputID}-error`;
  const aria = {
    "aria-required": required,
    "aria-invalid": missing,
    "aria-describedby": describedBy(required && hintID, missing && errorID),
  };
  const label = (
    <label htmlFor={inputID} className="block text-sm font-medium">
      {field.label}
      <RequiredMark active={required} testID={`${inputID}-required`} />
    </label>
  );
  const messages = (
    <>
      {required ? (
        <FieldMessage id={hintID}>Required before this step can be submitted.</FieldMessage>
      ) : null}
      {missing ? (
        <FieldMessage id={errorID} tone="error" testID={errorID}>
          Enter {field.label} before submitting this step.
        </FieldMessage>
      ) : null}
    </>
  );
  if (kind === "textarea") {
    return (
      <div className="space-y-2">
        {label}
        <Textarea
          id={inputID}
          value={typeof value === "string" ? value : ""}
          onChange={(event) => onChange(event.target.value)}
          placeholder={field.placeholder}
          {...aria}
          data-testid={inputID}
        />
        {messages}
      </div>
    );
  }
  if (kind === "select") {
    return (
      <div className="space-y-2">
        {label}
        <select
          id={inputID}
          className="flex h-10 w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
          value={typeof value === "string" ? value : ""}
          onChange={(event) => onChange(event.target.value)}
          {...aria}
          data-testid={inputID}
        >
          <option value="">Select</option>
          {(field.options ?? []).map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        {messages}
      </div>
    );
  }
  if (kind === "attachments") {
    const lines = Array.isArray(value)
      ? (value as Array<{ name?: unknown; uri?: unknown }>).map(
          (item) =>
            `${typeof item.name === "string" ? item.name : ""}|${typeof item.uri === "string" ? item.uri : ""}`,
        )
      : [];
    return (
      <div className="space-y-2">
        {label}
        <Textarea
          id={inputID}
          value={lines.join("\n")}
          onChange={(event) =>
            onChange(
              event.target.value
                .split("\n")
                .map((line) => line.trim())
                .filter(Boolean)
                .map((line) => {
                  const [name, uri] = line.split("|");
                  return { name: name?.trim() ?? "", uri: uri?.trim() ?? "" };
                }),
            )
          }
          placeholder={field.placeholder ?? "name|artifact://ref"}
          {...aria}
          data-testid={inputID}
        />
        {messages}
      </div>
    );
  }
  if (kind === "checkbox") {
    return (
      <div className="space-y-2">
        <div className="flex items-center gap-3 text-sm">
          <Checkbox
            id={inputID}
            checked={Boolean(value)}
            onChange={(event) => onChange(event.currentTarget.checked)}
            {...aria}
            data-testid={inputID}
          />
          <label htmlFor={inputID}>
            {field.label}
            <RequiredMark active={required} testID={`${inputID}-required`} />
          </label>
        </div>
        {messages}
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {label}
      <Input
        id={inputID}
        value={typeof value === "string" ? value : ""}
        onChange={(event) => onChange(event.target.value)}
        placeholder={field.placeholder}
        {...aria}
        data-testid={inputID}
      />
      {messages}
    </div>
  );
}
