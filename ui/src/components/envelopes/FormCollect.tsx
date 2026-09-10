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
  buildFormCollectCanonicalSeedKey,
  clearFormCollectDraft,
  FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS,
  loadFormCollectDraft,
  saveFormCollectDraft,
} from "@/lib/form-collect-draft-storage";
import {
  buildSubmitGate,
  describedBy,
  focusControl,
  useRevealRequirement,
} from "@/lib/submit-gate";

export type FormCollectFieldType =
  | "text"
  | "textarea"
  | "select"
  | "multiselect"
  | "radio"
  | "checkbox"
  | "number";

export type FormCollectCondition = {
  field_id: string;
  equals?: unknown;
  not_equals?: unknown;
  one_of?: unknown[];
  truthy?: boolean;
};

export type FormCollectOption = {
  value: string;
  label: string;
  help?: string;
};

export type FormCollectField = {
  id: string;
  type: FormCollectFieldType;
  label: string;
  help?: string;
  placeholder?: string;
  required?: boolean;
  default?: unknown;
  options?: FormCollectOption[];
  show_when?: FormCollectCondition;
};

export type FormCollectSection = {
  id: string;
  title: string;
  description?: string;
  repeatable?: boolean;
  min_items?: number;
  max_items?: number;
  show_when?: FormCollectCondition;
  fields: FormCollectField[];
};

export type FormCollectSchema = {
  fields?: FormCollectField[];
  sections?: FormCollectSection[];
};

export type FormCollectSavedDraft = {
  id: string;
  label: string;
  answers: Record<string, unknown>;
  notes?: string;
  saved_at?: string;
};

export type FormCollectTemplate = {
  id: string;
  label: string;
  answers: Record<string, unknown>;
  notes?: string;
};

export type FormCollectAction = {
  id: string;
  label: string;
  description?: string;
};

export type FormCollectAttachmentRef = {
  id?: string;
  name: string;
  artifact_id?: string;
  uri?: string;
  mime_type?: string;
  kind?: string;
  size_bytes?: number;
};

export type FormCollectSubmissionSummary = {
  submitted_at?: string;
  action_id?: string;
  answer_count?: number;
  attachment_count?: number;
  export_text?: string;
  export_name?: string;
};

export interface FormCollectEnvelope {
  v: number;
  id: string;
  type: "tangent.form-collect";
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    form_id?: string;
    intent?: string;
    schema?: FormCollectSchema;
    answers?: Record<string, unknown>;
    notes?: string;
    updated_at?: string;
    saved_drafts?: FormCollectSavedDraft[];
    templates?: FormCollectTemplate[];
    actions?: FormCollectAction[];
    attachment_refs?: FormCollectAttachmentRef[];
    submission_summary?: FormCollectSubmissionSummary;
  };
  meta?: Record<string, unknown>;
}

export interface FormCollectResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    form_id: string;
    answers: Record<string, unknown>;
    notes?: string;
    action_id?: string;
    saved_drafts?: FormCollectSavedDraft[];
    templates?: FormCollectTemplate[];
    attachment_refs?: FormCollectAttachmentRef[];
    submission_summary?: FormCollectSubmissionSummary;
  };
  completedAt?: string;
}

export type FormCollectProps = {
  envelope: FormCollectEnvelope;
  onSubmit: (response: FormCollectResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

const repeatableRowIDKey = "__row_id";

// Ids for the controls that are not part of the schema. They are constants
// because the submit gate points at the same strings the labels do, and a typo
// in either would silently break both "Go to …" and the label association.
const NOTES_ID = "form-collect-notes";
const ACTION_ID = "form-collect-action";
const ATTACHMENT_NAME_ID = "form-collect-attachment-name";

export function FormCollect({ envelope, onSubmit, onCancel, roomID }: FormCollectProps) {
  const formID = envelope.data?.form_id ?? "form";
  const schema = envelope.data?.schema ?? {};
  const initialAnswers = useMemo(
    () => normalizeAnswers(schema, envelope.data?.answers ?? {}),
    [schema, envelope.data?.answers],
  );
  const [answers, setAnswers] = useState<Record<string, unknown>>(initialAnswers);
  const [notes, setNotes] = useState<string>(envelope.data?.notes ?? "");
  const [actionID, setActionID] = useState<string>("");
  const [savedDrafts, setSavedDrafts] = useState<FormCollectSavedDraft[]>(
    envelope.data?.saved_drafts ?? [],
  );
  const [templates, setTemplates] = useState<FormCollectTemplate[]>(envelope.data?.templates ?? []);
  const [attachmentRefs, setAttachmentRefs] = useState<FormCollectAttachmentRef[]>(
    envelope.data?.attachment_refs ?? [],
  );
  const [draftLabel, setDraftLabel] = useState("");
  const [templateLabel, setTemplateLabel] = useState("");
  const [attachmentDraft, setAttachmentDraft] = useState<FormCollectAttachmentRef>({ name: "" });
  const [message, setMessage] = useState<string | null>(null);
  const [summaryCopied, setSummaryCopied] = useState(false);
  // "Add attachment ref" used to return silently when the display name was
  // empty. The flag turns that dead click into a visible, focused refusal.
  const [attachmentAttempted, setAttachmentAttempted] = useState(false);

  const revealRequirement = useRevealRequirement();

  const seedKey = useMemo(
    () =>
      buildFormCollectCanonicalSeedKey({
        formID,
        schema,
        answers: sanitizeAnswersForSubmit(initialAnswers),
        notes: envelope.data?.notes ?? "",
        templates: envelope.data?.templates ?? [],
      }),
    [formID, schema, initialAnswers, envelope.data?.notes, envelope.data?.templates],
  );

  useEffect(() => {
    setAnswers(initialAnswers);
    setNotes(envelope.data?.notes ?? "");
    setSavedDrafts(envelope.data?.saved_drafts ?? []);
    setTemplates(envelope.data?.templates ?? []);
    setAttachmentRefs(envelope.data?.attachment_refs ?? []);
    setActionID("");
  }, [
    initialAnswers,
    envelope.data?.notes,
    envelope.data?.saved_drafts,
    envelope.data?.templates,
    envelope.data?.attachment_refs,
  ]);

  useEffect(() => {
    if (!roomID || !formID) {
      return;
    }
    const recovered = loadFormCollectDraft(roomID, formID);
    if (!recovered || recovered.envelopeId !== envelope.id || recovered.baseSeedKey !== seedKey) {
      return;
    }
    setAnswers(normalizeAnswers(schema, recovered.answers));
    setNotes(recovered.notes);
    setActionID(recovered.actionID);
    setSavedDrafts((recovered.savedDrafts as FormCollectSavedDraft[]) ?? []);
    setTemplates((recovered.templates as FormCollectTemplate[]) ?? []);
    setAttachmentRefs((recovered.attachmentRefs as FormCollectAttachmentRef[]) ?? []);
    setMessage("Recovered unsent form state from this browser.");
  }, [roomID, formID, envelope.id, seedKey, schema]);

  useEffect(() => {
    if (!roomID || !formID) {
      return;
    }
    const handle = window.setTimeout(() => {
      saveFormCollectDraft({
        version: 1,
        roomID,
        formID,
        envelopeId: envelope.id,
        baseSeedKey: seedKey,
        answers,
        notes,
        actionID,
        savedDrafts: savedDrafts as Array<Record<string, unknown>>,
        templates: templates as Array<Record<string, unknown>>,
        attachmentRefs: attachmentRefs as Array<Record<string, unknown>>,
        savedAt: new Date().toISOString(),
      });
    }, FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(handle);
  }, [
    roomID,
    formID,
    envelope.id,
    seedKey,
    answers,
    notes,
    actionID,
    savedDrafts,
    templates,
    attachmentRefs,
  ]);

  const validation = evaluateCompletion(schema, answers);
  const summary = envelope.data?.submission_summary;
  const outstanding = validation.outstanding;
  const attachmentNameMissing =
    attachmentAttempted && (attachmentDraft.name ?? "").trim().length === 0;

  // What the operator still owes, named. `requiredRemaining` on its own could
  // only dim the button, and that is what made this form's worst case
  // unreadable: a `show_when` section can reveal a whole block of required
  // fields from a control several screens above, so the count jumped and
  // Submit died with nothing on screen saying which field had just appeared.
  // The gate carries the first outstanding field's own label and the id its
  // label points at. No `reveal` step — the form is one scrolling column, so
  // scrolling to the control and focusing it is the whole journey.
  //
  // The missing form id is the other half. `handleSubmit` has always refused to
  // fire without one, but `disabled` never reflected that, so an envelope with
  // an empty `form_id` rendered a live button that did nothing at all.
  const gate = buildSubmitGate([
    // One requirement per outstanding field rather than one aggregate: the
    // notice then names the field the operator is being sent to, and the
    // header's "N required remaining" count carries the total.
    ...outstanding.map((entry) => ({
      controlID: entry.controlID,
      label: entry.label,
      message: `"${entry.label}" is required and still empty.`,
    })),
    !formID && {
      controlID: "",
      label: "form",
      message: "this envelope carries no form id, so a submission cannot be recorded against it.",
    },
  ]);
  // Gating semantics are unchanged: the button is disabled by the required
  // field count exactly as before. The form-id requirement is surfaced through
  // the notice on a CTA that stays live, and enforced in the handler.
  const submitDisabled = validation.requiredRemaining > 0;

  const handleSubmit = () => {
    if (gate.blocked) {
      revealRequirement(gate.first);
      return;
    }
    if (roomID) {
      clearFormCollectDraft(roomID, formID);
    }
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        form_id: formID,
        answers: sanitizeAnswersForSubmit(answers),
        notes: notes || undefined,
        action_id: actionID || undefined,
        saved_drafts: savedDrafts,
        templates,
        attachment_refs: attachmentRefs,
        submission_summary: summary,
      },
      completedAt: new Date().toISOString(),
    });
  };

  const handleCancel = () => {
    if (roomID) {
      clearFormCollectDraft(roomID, formID);
    }
    onCancel();
  };

  return (
    <Card data-testid="form-collect-root" className="w-full max-w-4xl">
      <CardHeader className="space-y-2">
        <CardTitle className="text-lg">{envelope.title ?? "Form collect"}</CardTitle>
        {envelope.data?.intent ? (
          <Markdown content={envelope.data.intent} className="text-zinc-300" />
        ) : null}
        {envelope.context ? (
          <Markdown content={envelope.context} className="text-zinc-500" />
        ) : null}
        <p className="text-xs text-zinc-500">
          {validation.visibleCount} visible field{validation.visibleCount === 1 ? "" : "s"}
          {validation.requiredRemaining > 0
            ? ` • ${validation.requiredRemaining} required remaining`
            : " • ready to submit"}
        </p>
        {message ? (
          <div
            data-testid="form-collect-message"
            role="status"
            aria-live="polite"
            className="rounded-md border border-emerald-800 bg-emerald-950/40 px-3 py-2 text-sm text-emerald-200"
          >
            {message}
          </div>
        ) : null}
        {summary ? (
          <div className="rounded-md border border-zinc-800 bg-zinc-950/50 p-3 text-sm text-zinc-300">
            <p className="font-medium text-zinc-100">Last submission</p>
            <p>
              {summary.submitted_at ?? "unknown time"} • {summary.answer_count ?? 0} answers •{" "}
              {summary.attachment_count ?? 0} attachments
            </p>
            {summary.action_id ? <p>Action: {summary.action_id}</p> : null}
            {summary.export_text ? (
              <div className="mt-2 space-y-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  data-testid="form-collect-copy-summary"
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(summary.export_text ?? "");
                      setSummaryCopied(true);
                    } catch {
                      setSummaryCopied(false);
                    }
                  }}
                >
                  Copy summary export
                </Button>
                {summaryCopied ? <p className="text-xs text-emerald-300">Copied.</p> : null}
              </div>
            ) : null}
          </div>
        ) : null}
      </CardHeader>

      <CardContent className="space-y-6">
        {schema.fields?.map((field) => (
          <FieldRenderer
            key={field.id}
            field={field}
            controlId={field.id}
            value={answers[field.id]}
            rootAnswers={answers}
            onChange={(value) => setAnswers((current) => ({ ...current, [field.id]: value }))}
          />
        ))}

        {schema.sections?.map((section) => (
          <SectionRenderer
            key={section.id}
            section={section}
            rootAnswers={answers}
            value={answers[section.id]}
            onChange={(value) => setAnswers((current) => ({ ...current, [section.id]: value }))}
          />
        ))}

        {/*
          Notes sat in a bordered section under a heading styled exactly like a
          required field's label, so it read as one more thing the form was
          waiting on. It is a real label now, and the hint says outright that it
          is optional and not one of the schema's fields.
        */}
        <section className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
          <label className="block text-sm font-medium text-zinc-100" htmlFor={NOTES_ID}>
            Notes
          </label>
          <Textarea
            id={NOTES_ID}
            value={notes}
            onChange={(event) => setNotes(event.currentTarget.value)}
            placeholder="Optional submission notes"
            aria-describedby={`${NOTES_ID}-hint`}
            data-testid="form-collect-notes"
          />
          <FieldMessage id={`${NOTES_ID}-hint`}>
            Optional. A freeform note kept alongside the submission — never one of the form's
            fields, and never required.
          </FieldMessage>
        </section>

        {envelope.data?.actions && envelope.data.actions.length > 0 ? (
          <section className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
            <label className="block text-sm font-medium text-zinc-100" htmlFor={ACTION_ID}>
              Submit action
            </label>
            <select
              id={ACTION_ID}
              value={actionID}
              onChange={(event) => setActionID(event.currentTarget.value)}
              aria-describedby={`${ACTION_ID}-hint`}
              data-testid="form-collect-action"
              className="h-10 w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 text-sm text-zinc-100"
            >
              <option value="">Default submit</option>
              {envelope.data.actions.map((action) => (
                <option key={action.id} value={action.id}>
                  {action.label}
                </option>
              ))}
            </select>
            <FieldMessage id={`${ACTION_ID}-hint`}>
              Optional. Leaving the default submits the form without naming an action.
            </FieldMessage>
          </section>
        ) : null}

        <SavedItemsEditor
          title="Saved drafts"
          controlID="form-collect-draft-label"
          fieldLabel="Draft name"
          inputLabel={draftLabel}
          setInputLabel={setDraftLabel}
          items={savedDrafts}
          onSave={() => {
            const label = draftLabel.trim();
            if (!label) {
              return;
            }
            const now = new Date().toISOString();
            setSavedDrafts((current) => [
              {
                id: slugify(label, now),
                label,
                answers,
                notes,
                saved_at: now,
              },
              ...current.filter((item) => item.label !== label),
            ]);
            setDraftLabel("");
          }}
          onRestore={(item) => {
            setAnswers(normalizeAnswers(schema, item.answers));
            setNotes(item.notes ?? "");
            setMessage(`Restored draft "${item.label}".`);
          }}
        />

        <SavedItemsEditor
          title="Templates"
          controlID="form-collect-template-label"
          fieldLabel="Template name"
          inputLabel={templateLabel}
          setInputLabel={setTemplateLabel}
          items={templates}
          onSave={() => {
            const label = templateLabel.trim();
            if (!label) {
              return;
            }
            setTemplates((current) => [
              {
                id: slugify(label, String(current.length + 1)),
                label,
                answers,
                notes,
              },
              ...current.filter((item) => item.label !== label),
            ]);
            setTemplateLabel("");
          }}
          onRestore={(item) => {
            setAnswers(normalizeAnswers(schema, item.answers));
            setNotes(item.notes ?? "");
            setMessage(`Applied template "${item.label}".`);
          }}
        />

        <section className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
          <p className="text-sm font-medium text-zinc-100">Attachment refs</p>
          <div className="grid gap-3 md:grid-cols-2">
            {/*
              The display name is the one thing "Add attachment ref" actually
              requires, and it was an anonymous box in a row of anonymous boxes:
              no id, no label, and a click that returned silently when it was
              empty. It is marked required and reports the refusal in place.
            */}
            <div className="space-y-1">
              <label className="block text-xs text-zinc-400" htmlFor={ATTACHMENT_NAME_ID}>
                Display name
                <RequiredMark testID="form-collect-attachment-name-required" />
              </label>
              <Input
                id={ATTACHMENT_NAME_ID}
                value={attachmentDraft.name ?? ""}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setAttachmentAttempted(false);
                  setAttachmentDraft((current) => ({ ...current, name: value }));
                }}
                placeholder="Display name"
                aria-required="true"
                aria-invalid={attachmentNameMissing}
                aria-describedby={describedBy(
                  `${ATTACHMENT_NAME_ID}-hint`,
                  attachmentNameMissing && `${ATTACHMENT_NAME_ID}-error`,
                )}
                data-testid="form-collect-attachment-name"
              />
              <FieldMessage id={`${ATTACHMENT_NAME_ID}-hint`}>
                Required. Each attachment ref is listed and removed under this name.
              </FieldMessage>
              {attachmentNameMissing ? (
                <FieldMessage
                  id={`${ATTACHMENT_NAME_ID}-error`}
                  tone="error"
                  testID="form-collect-attachment-name-error"
                >
                  Enter a display name before adding the attachment ref.
                </FieldMessage>
              ) : null}
            </div>
            <AttachmentFieldInput
              id="form-collect-attachment-uri"
              label="URI"
              value={attachmentDraft.uri ?? ""}
              placeholder="artifact://…"
              testID="form-collect-attachment-uri"
              onChange={(value) => setAttachmentDraft((current) => ({ ...current, uri: value }))}
            />
            <AttachmentFieldInput
              id="form-collect-attachment-mime"
              label="MIME type"
              value={attachmentDraft.mime_type ?? ""}
              placeholder="application/pdf"
              testID="form-collect-attachment-mime"
              onChange={(value) =>
                setAttachmentDraft((current) => ({ ...current, mime_type: value }))
              }
            />
            <AttachmentFieldInput
              id="form-collect-attachment-kind"
              label="Kind"
              value={attachmentDraft.kind ?? ""}
              placeholder="spec, transcript, …"
              testID="form-collect-attachment-kind"
              onChange={(value) => setAttachmentDraft((current) => ({ ...current, kind: value }))}
            />
          </div>
          <Button
            type="button"
            variant="outline"
            data-testid="form-collect-add-attachment"
            onClick={() => {
              if (!attachmentDraft.name?.trim()) {
                setAttachmentAttempted(true);
                focusControl(ATTACHMENT_NAME_ID);
                return;
              }
              setAttachmentAttempted(false);
              setAttachmentRefs((current) => [
                ...current,
                {
                  ...attachmentDraft,
                  id:
                    attachmentDraft.id ??
                    slugify(attachmentDraft.name ?? "attachment", String(current.length + 1)),
                },
              ]);
              setAttachmentDraft({ name: "" });
            }}
          >
            Add attachment ref
          </Button>
          <div className="space-y-2">
            {attachmentRefs.map((item) => (
              <div
                key={item.id ?? item.name}
                className="flex items-center justify-between rounded border border-zinc-800 px-3 py-2 text-sm text-zinc-300"
              >
                <div>
                  <p>{item.name}</p>
                  {item.uri ? <p className="text-xs text-zinc-500">{item.uri}</p> : null}
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    setAttachmentRefs((current) =>
                      current.filter(
                        (candidate) => (candidate.id ?? candidate.name) !== (item.id ?? item.name),
                      ),
                    )
                  }
                >
                  Remove
                </Button>
              </div>
            ))}
          </div>
        </section>
      </CardContent>

      <CardFooter className="flex-wrap justify-end gap-3">
        <SubmitGateNotice
          gate={gate}
          testID="form-collect-submit-gate"
          action="Submit form"
          mode={submitDisabled ? "disabled" : "attempt"}
          onReveal={revealRequirement}
        />
        <Button
          type="button"
          variant="ghost"
          onClick={handleCancel}
          data-testid="form-collect-cancel"
        >
          Cancel
        </Button>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={submitDisabled}
          data-testid="form-collect-submit"
          aria-describedby={gate.blocked ? "form-collect-submit-gate" : undefined}
        >
          Submit form
        </Button>
      </CardFooter>
    </Card>
  );
}

function FieldRenderer({
  field,
  controlId,
  value,
  rootAnswers,
  onChange,
}: {
  field: FormCollectField;
  controlId: string;
  value: unknown;
  rootAnswers: Record<string, unknown>;
  onChange: (value: unknown) => void;
}) {
  if (!isVisible(field.show_when, rootAnswers)) {
    return null;
  }

  const currentValue = value ?? defaultFieldValue(field);
  const isGrouped = isGroupedFieldType(field.type);
  // `required` is a schema fact that used to reach the operator as a bare red
  // asterisk with no legend anywhere on the page, and never reached a screen
  // reader at all. The badge says the word, the control carries it in aria, and
  // `field.help` finally has an id so the control can point at it.
  const required = Boolean(field.required);
  const missing = required && !isFilled(field, currentValue);
  const hintID = `${controlId}-hint`;
  const errorID = `${controlId}-error`;
  const help = field.help ? <FieldMessage id={hintID}>{field.help}</FieldMessage> : null;
  const control = (
    <FieldControl
      field={field}
      controlId={controlId}
      value={currentValue}
      required={required}
      invalid={missing}
      describedByIDs={describedBy(field.help ? hintID : undefined, missing && errorID)}
      onChange={onChange}
    />
  );
  const error = missing ? (
    <FieldMessage id={errorID} tone="error" testID={`form-collect-error-${controlId}`}>
      {field.label} is required and still empty.
    </FieldMessage>
  ) : null;

  if (isGrouped) {
    return (
      <fieldset
        className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4"
        data-testid={`form-collect-field-${field.id}`}
      >
        <legend className="text-sm font-medium text-zinc-100">
          {field.label}
          <RequiredMark active={required} testID={`form-collect-required-${controlId}`} />
        </legend>
        {help}
        {control}
        {error}
      </fieldset>
    );
  }

  return (
    <section
      className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4"
      data-testid={`form-collect-field-${field.id}`}
    >
      <div className="space-y-1">
        <label htmlFor={controlId} className="text-sm font-medium text-zinc-100">
          {field.label}
          <RequiredMark active={required} testID={`form-collect-required-${controlId}`} />
        </label>
        {help}
      </div>
      {control}
      {error}
    </section>
  );
}

function SectionRenderer({
  section,
  rootAnswers,
  value,
  onChange,
}: {
  section: FormCollectSection;
  rootAnswers: Record<string, unknown>;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  if (!isVisible(section.show_when, rootAnswers)) {
    return null;
  }

  if (section.repeatable) {
    const rows = Array.isArray(value) ? (value as Record<string, unknown>[]) : [];
    const minItems = section.min_items ?? 0;
    const maxItems = section.max_items ?? Number.POSITIVE_INFINITY;
    const visibleRows = rows.length > 0 ? rows : minItems > 0 ? buildRows(section, minItems) : [];
    return (
      <section
        className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4"
        data-testid={`form-collect-section-${section.id}`}
      >
        <div className="space-y-1">
          <p className="text-sm font-medium text-zinc-100">{section.title}</p>
          {section.description ? (
            <Markdown content={section.description} className="text-xs text-zinc-400" />
          ) : null}
        </div>
        {visibleRows.map((row, index) => (
          <div
            key={repeatableRowKey(section.id, row, index)}
            className="space-y-3 rounded-md border border-zinc-800 p-3"
          >
            <div className="flex items-center justify-between">
              <p className="text-xs uppercase tracking-wide text-zinc-500">
                {section.title} #{index + 1}
              </p>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={visibleRows.length <= minItems}
                data-testid={`form-collect-remove-row-${section.id}-${index}`}
                onClick={() => onChange(visibleRows.filter((_, candidate) => candidate !== index))}
              >
                Remove
              </Button>
            </div>
            {section.fields.map((field) => (
              <FieldRenderer
                key={`${repeatableRowKey(section.id, row, index)}-${field.id}`}
                field={field}
                controlId={`${section.id}-${field.id}-${repeatableRowKey(section.id, row, index)}`}
                value={row[field.id]}
                rootAnswers={{ ...rootAnswers, ...row }}
                onChange={(next) =>
                  onChange(
                    visibleRows.map((candidate, candidateIndex) =>
                      candidateIndex === index ? { ...candidate, [field.id]: next } : candidate,
                    ),
                  )
                }
              />
            ))}
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          disabled={visibleRows.length >= maxItems}
          data-testid={`form-collect-add-row-${section.id}`}
          onClick={() => onChange([...visibleRows, buildRow(section)])}
        >
          Add {section.title.toLowerCase()}
        </Button>
      </section>
    );
  }

  const record =
    value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : buildRow(section);
  return (
    <section
      className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4"
      data-testid={`form-collect-section-${section.id}`}
    >
      <div className="space-y-1">
        <p className="text-sm font-medium text-zinc-100">{section.title}</p>
        {section.description ? (
          <Markdown content={section.description} className="text-xs text-zinc-400" />
        ) : null}
      </div>
      {section.fields.map((field) => (
        <FieldRenderer
          key={`${section.id}-${field.id}`}
          field={field}
          controlId={`${section.id}-${field.id}`}
          value={record[field.id]}
          rootAnswers={{ ...rootAnswers, ...record }}
          onChange={(next) => onChange({ ...record, [field.id]: next })}
        />
      ))}
    </section>
  );
}

function FieldControl({
  field,
  controlId,
  value,
  required,
  invalid,
  describedByIDs,
  onChange,
}: {
  field: FormCollectField;
  controlId: string;
  value: unknown;
  required: boolean;
  invalid: boolean;
  describedByIDs: string | undefined;
  onChange: (value: unknown) => void;
}) {
  // Grouped types put their ids on the options rather than on the field, so the
  // aria goes on every option: that is what a screen reader reads as it arrows
  // through them, and it is what `focusTargetFor` sends the gate to.
  const aria = {
    "aria-required": required,
    "aria-invalid": invalid,
    "aria-describedby": describedByIDs,
  };
  switch (field.type) {
    case "textarea":
      return (
        <Textarea
          id={controlId}
          value={String(value ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onChange(event.currentTarget.value)}
          {...aria}
          data-testid={`form-collect-input-${field.id}`}
        />
      );
    case "select":
      return (
        <select
          id={controlId}
          value={String(value ?? "")}
          onChange={(event) => onChange(event.currentTarget.value)}
          {...aria}
          data-testid={`form-collect-input-${field.id}`}
          className="h-10 w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 text-sm text-zinc-100"
        >
          <option value="">Select…</option>
          {field.options?.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      );
    case "multiselect": {
      const values = Array.isArray(value) ? (value as string[]) : [];
      return (
        <div className="space-y-2">
          {field.options?.map((option) => (
            <div key={option.value} className="flex items-start gap-2 text-sm text-zinc-200">
              <Checkbox
                id={`${controlId}-${option.value}`}
                checked={values.includes(option.value)}
                {...aria}
                onChange={(event) =>
                  onChange(
                    event.currentTarget.checked
                      ? [...values, option.value]
                      : values.filter((candidate) => candidate !== option.value),
                  )
                }
              />
              <label htmlFor={`${controlId}-${option.value}`}>{option.label}</label>
            </div>
          ))}
        </div>
      );
    }
    case "radio":
      return (
        <div className="space-y-2">
          {field.options?.map((option) => (
            <div key={option.value} className="flex items-start gap-2 text-sm text-zinc-200">
              <input
                id={`${controlId}-${option.value}`}
                type="radio"
                name={controlId}
                checked={String(value ?? "") === option.value}
                onChange={() => onChange(option.value)}
                {...aria}
                data-testid={`form-collect-radio-${field.id}-${option.value}`}
              />
              <label htmlFor={`${controlId}-${option.value}`}>{option.label}</label>
            </div>
          ))}
        </div>
      );
    case "checkbox":
      // The checkbox's own `<label htmlFor>` used to read `placeholder ?? "Checked"`,
      // which made "Checked" the control's accessible name while the thing being
      // agreed to sat in a `<legend>` the checkbox never pointed at. The field
      // label is now the single label above (see `isGroupedFieldType`), and the
      // placeholder stays as visible choice wording beside the box.
      return (
        <div className="flex items-center gap-2 text-sm text-zinc-200">
          <Checkbox
            id={controlId}
            checked={Boolean(value)}
            onChange={(event) => onChange(event.currentTarget.checked)}
            {...aria}
            data-testid={`form-collect-input-${field.id}`}
          />
          {field.placeholder ? <span className="text-zinc-400">{field.placeholder}</span> : null}
        </div>
      );
    case "number":
      return (
        <Input
          id={controlId}
          type="number"
          value={String(value ?? "")}
          placeholder={field.placeholder}
          onChange={(event) =>
            onChange(event.currentTarget.value === "" ? "" : Number(event.currentTarget.value))
          }
          {...aria}
          data-testid={`form-collect-input-${field.id}`}
        />
      );
    default:
      return (
        <Input
          id={controlId}
          value={String(value ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onChange(event.currentTarget.value)}
          {...aria}
          data-testid={`form-collect-input-${field.id}`}
        />
      );
  }
}

/**
 * One optional field in the attachment-ref editor.
 *
 * The four inputs were placeholder-only boxes; a placeholder is not a label and
 * disappears the moment anyone types. Only the display name is required, so the
 * required affordances live at the call site and this covers the rest.
 */
function AttachmentFieldInput({
  id,
  label,
  value,
  placeholder,
  testID,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  placeholder: string;
  testID: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="space-y-1">
      <label className="block text-xs text-zinc-400" htmlFor={id}>
        {label}
      </label>
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.currentTarget.value)}
        data-testid={testID}
      />
    </div>
  );
}

function SavedItemsEditor<
  T extends { id: string; label: string; answers: Record<string, unknown>; notes?: string },
>({
  title,
  controlID,
  fieldLabel,
  inputLabel,
  setInputLabel,
  items,
  onSave,
  onRestore,
}: {
  title: string;
  controlID: string;
  fieldLabel: string;
  inputLabel: string;
  setInputLabel: (value: string) => void;
  items: T[];
  onSave: () => void;
  onRestore: (item: T) => void;
}) {
  // Save returned silently when the name was blank, and the input it read had
  // no id, no label and no placeholder — a dead button beside an anonymous box.
  // The name is what Save requires, so it says so, and an empty Save reports
  // the refusal on the control instead of doing nothing.
  const [attempted, setAttempted] = useState(false);
  const missing = attempted && inputLabel.trim().length === 0;
  return (
    <section className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
      <p className="text-sm font-medium text-zinc-100">{title}</p>
      <div className="space-y-1">
        <label className="block text-xs text-zinc-400" htmlFor={controlID}>
          {fieldLabel}
          <RequiredMark testID={`${controlID}-required`} />
        </label>
        <div className="flex gap-2">
          <Input
            id={controlID}
            value={inputLabel}
            onChange={(event) => {
              setAttempted(false);
              setInputLabel(event.currentTarget.value);
            }}
            placeholder={fieldLabel}
            aria-required="true"
            aria-invalid={missing}
            aria-describedby={describedBy(`${controlID}-hint`, missing && `${controlID}-error`)}
            data-testid={controlID}
          />
          <Button
            type="button"
            variant="outline"
            data-testid={`${controlID}-save`}
            onClick={() => {
              if (inputLabel.trim().length === 0) {
                setAttempted(true);
                focusControl(controlID);
                return;
              }
              setAttempted(false);
              onSave();
            }}
          >
            Save
          </Button>
        </div>
        <FieldMessage id={`${controlID}-hint`}>
          Required. {title} are listed and restored under this name.
        </FieldMessage>
        {missing ? (
          <FieldMessage id={`${controlID}-error`} tone="error" testID={`${controlID}-error`}>
            Enter a {fieldLabel.toLowerCase()} before saving.
          </FieldMessage>
        ) : null}
      </div>
      <div className="space-y-2">
        {items.map((item) => (
          <div
            key={item.id}
            className="flex items-center justify-between rounded border border-zinc-800 px-3 py-2 text-sm"
          >
            <div>
              <p className="text-zinc-100">{item.label}</p>
              {item.notes ? <p className="text-xs text-zinc-500">{item.notes}</p> : null}
            </div>
            <Button type="button" variant="ghost" size="sm" onClick={() => onRestore(item)}>
              Restore
            </Button>
          </div>
        ))}
      </div>
    </section>
  );
}

function normalizeAnswers(
  schema: FormCollectSchema,
  answers: Record<string, unknown>,
): Record<string, unknown> {
  const next = { ...answers };
  for (const field of schema.fields ?? []) {
    if (next[field.id] === undefined) {
      next[field.id] = defaultFieldValue(field);
    }
  }
  for (const section of schema.sections ?? []) {
    if (section.repeatable) {
      const current = Array.isArray(next[section.id])
        ? (next[section.id] as Record<string, unknown>[])
        : [];
      const rows = current.map((row) => normalizeSectionRow(section, row));
      const minItems = section.min_items ?? 0;
      while (rows.length < minItems) {
        rows.push(buildRow(section));
      }
      next[section.id] = rows;
    } else {
      const current =
        next[section.id] && typeof next[section.id] === "object" && !Array.isArray(next[section.id])
          ? (next[section.id] as Record<string, unknown>)
          : {};
      next[section.id] = normalizeSectionRow(section, current);
    }
  }
  return next;
}

function normalizeSectionRow(
  section: FormCollectSection,
  row: Record<string, unknown>,
): Record<string, unknown> {
  const next: Record<string, unknown> = {
    [repeatableRowIDKey]: readRepeatableRowID(row),
    ...row,
  };
  for (const field of section.fields) {
    if (next[field.id] === undefined) {
      next[field.id] = defaultFieldValue(field);
    }
  }
  return next;
}

function defaultFieldValue(field: FormCollectField): unknown {
  if (field.default !== undefined) {
    return field.default;
  }
  switch (field.type) {
    case "checkbox":
      return false;
    case "multiselect":
      return [];
    default:
      return "";
  }
}

// A lone checkbox is not a group. Rendering it in a `<fieldset>` pushed its
// accessible name onto an inline label that read "Checked" while the field's
// own label sat in a `<legend>` the control never referenced; it renders like
// every other single-value field now, with one `<label htmlFor>`.
function isGroupedFieldType(type: FormCollectFieldType): boolean {
  return type === "multiselect" || type === "radio";
}

/** A required field that is still empty, named the way the operator sees it. */
type OutstandingRequired = {
  /** The DOM id the gate scrolls to and focuses. */
  controlID: string;
  /** The field's own label, plus its row when the section repeats. */
  label: string;
};

function evaluateCompletion(schema: FormCollectSchema, answers: Record<string, unknown>) {
  let visibleCount = 0;
  let requiredRemaining = 0;
  // Collected in the same walk as the count so the notice can never name a
  // field the count disagrees about.
  const outstanding: OutstandingRequired[] = [];
  for (const field of schema.fields ?? []) {
    if (!isVisible(field.show_when, answers)) {
      continue;
    }
    visibleCount += 1;
    if (field.required && !isFilled(field, answers[field.id])) {
      requiredRemaining += 1;
      outstanding.push({ controlID: focusTargetFor(field, field.id), label: field.label });
    }
  }
  for (const section of schema.sections ?? []) {
    if (!isVisible(section.show_when, answers)) {
      continue;
    }
    const rows = section.repeatable
      ? Array.isArray(answers[section.id])
        ? (answers[section.id] as Record<string, unknown>[])
        : []
      : [(answers[section.id] as Record<string, unknown>) ?? {}];
    rows.forEach((row, index) => {
      for (const field of section.fields) {
        if (!isVisible(field.show_when, { ...answers, ...row })) {
          continue;
        }
        visibleCount += 1;
        if (field.required && !isFilled(field, row[field.id])) {
          requiredRemaining += 1;
          const controlId = section.repeatable
            ? `${section.id}-${field.id}-${repeatableRowKey(section.id, row, index)}`
            : `${section.id}-${field.id}`;
          outstanding.push({
            controlID: focusTargetFor(field, controlId),
            label: section.repeatable
              ? `${field.label} (${section.title} #${index + 1})`
              : field.label,
          });
        }
      }
    });
  }
  return { visibleCount, requiredRemaining, outstanding };
}

/**
 * The id the gate can actually focus for a field.
 *
 * Grouped controls carry no id of their own — each option owns one — so the
 * first option stands in for the group.
 */
function focusTargetFor(field: FormCollectField, controlId: string): string {
  if (!isGroupedFieldType(field.type)) {
    return controlId;
  }
  const first = field.options?.[0]?.value;
  return first === undefined ? controlId : `${controlId}-${first}`;
}

function isVisible(
  condition: FormCollectCondition | undefined,
  answers: Record<string, unknown>,
): boolean {
  if (!condition) {
    return true;
  }
  const value = answers[condition.field_id];
  if (condition.truthy) {
    return Boolean(value);
  }
  if (condition.equals !== undefined) {
    return JSON.stringify(value) === JSON.stringify(condition.equals);
  }
  if (condition.not_equals !== undefined) {
    return JSON.stringify(value) !== JSON.stringify(condition.not_equals);
  }
  if (condition.one_of) {
    return condition.one_of.some(
      (candidate) => JSON.stringify(candidate) === JSON.stringify(value),
    );
  }
  return true;
}

function isFilled(field: FormCollectField, value: unknown): boolean {
  switch (field.type) {
    case "checkbox":
      return value === true;
    case "multiselect":
      return Array.isArray(value) && value.length > 0;
    default:
      return typeof value === "string"
        ? value.trim().length > 0
        : value !== undefined && value !== null;
  }
}

function buildRows(section: FormCollectSection, count: number): Record<string, unknown>[] {
  return Array.from({ length: count }, () => buildRow(section));
}

function buildRow(section: FormCollectSection): Record<string, unknown> {
  return {
    [repeatableRowIDKey]: createRepeatableRowID(section.id),
    ...Object.fromEntries(section.fields.map((field) => [field.id, defaultFieldValue(field)])),
  };
}

/**
 * Stable key for one repeatable row.
 *
 * This used to fall through to `readRepeatableRowID`, which mints a fresh
 * `Math.random()` id whenever `__row_id` is absent — so a row without one got a
 * new id on every render and its `<label htmlFor>` stopped pointing at its
 * control. Rows held in state always carry `__row_id` (normalizeSectionRow
 * assigns it once); the index keeps the fallback stable for the rows
 * SectionRenderer synthesises to satisfy `min_items`.
 */
function repeatableRowKey(sectionID: string, row: Record<string, unknown>, index: number): string {
  const value = row[repeatableRowIDKey];
  const rowID = typeof value === "string" && value.length > 0 ? value : `row-${index}`;
  return `${sectionID}-${rowID}`;
}

function readRepeatableRowID(row: Record<string, unknown>): string {
  const value = row[repeatableRowIDKey];
  return typeof value === "string" && value.length > 0 ? value : createRepeatableRowID("row");
}

function createRepeatableRowID(prefix: string): string {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

function sanitizeAnswersForSubmit(value: unknown): Record<string, unknown> {
  const sanitized = sanitizeAnswerValue(value);
  return sanitized && typeof sanitized === "object" && !Array.isArray(sanitized)
    ? (sanitized as Record<string, unknown>)
    : {};
}

function sanitizeAnswerValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => sanitizeAnswerValue(item));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .filter(([key]) => key !== repeatableRowIDKey)
        .map(([key, child]) => [key, sanitizeAnswerValue(child)]),
    );
  }
  return value;
}

function slugify(input: string, suffix: string): string {
  return `${input
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")}-${suffix}`;
}
