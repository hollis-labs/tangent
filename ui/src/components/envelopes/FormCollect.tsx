import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  buildFormCollectCanonicalSeedKey,
  clearFormCollectDraft,
  FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS,
  loadFormCollectDraft,
  saveFormCollectDraft,
} from "@/lib/form-collect-draft-storage";

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

  const handleSubmit = () => {
    if (!formID || validation.requiredRemaining > 0) {
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
          <p className="text-sm text-zinc-300">{envelope.data.intent}</p>
        ) : null}
        {envelope.context ? (
          <p className="whitespace-pre-wrap text-sm text-zinc-500">{envelope.context}</p>
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

        <section className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
          <p className="text-sm font-medium text-zinc-100">Notes</p>
          <Textarea
            value={notes}
            onChange={(event) => setNotes(event.currentTarget.value)}
            placeholder="Optional submission notes"
            data-testid="form-collect-notes"
          />
        </section>

        {envelope.data?.actions && envelope.data.actions.length > 0 ? (
          <section className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
            <p className="text-sm font-medium text-zinc-100">Submit action</p>
            <select
              value={actionID}
              onChange={(event) => setActionID(event.currentTarget.value)}
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
          </section>
        ) : null}

        <SavedItemsEditor
          title="Saved drafts"
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
          <div className="grid gap-2 md:grid-cols-2">
            <Input
              value={attachmentDraft.name ?? ""}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setAttachmentDraft((current) => ({ ...current, name: value }));
              }}
              placeholder="Display name"
              data-testid="form-collect-attachment-name"
            />
            <Input
              value={attachmentDraft.uri ?? ""}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setAttachmentDraft((current) => ({ ...current, uri: value }));
              }}
              placeholder="URI"
              data-testid="form-collect-attachment-uri"
            />
            <Input
              value={attachmentDraft.mime_type ?? ""}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setAttachmentDraft((current) => ({ ...current, mime_type: value }));
              }}
              placeholder="MIME type"
              data-testid="form-collect-attachment-mime"
            />
            <Input
              value={attachmentDraft.kind ?? ""}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setAttachmentDraft((current) => ({ ...current, kind: value }));
              }}
              placeholder="Kind"
              data-testid="form-collect-attachment-kind"
            />
          </div>
          <Button
            type="button"
            variant="outline"
            data-testid="form-collect-add-attachment"
            onClick={() => {
              if (!attachmentDraft.name?.trim()) {
                return;
              }
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

      <CardFooter className="justify-end gap-3">
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
          disabled={validation.requiredRemaining > 0}
          data-testid="form-collect-submit"
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

  if (isGrouped) {
    return (
      <fieldset
        className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4"
        data-testid={`form-collect-field-${field.id}`}
      >
        <legend className="text-sm font-medium text-zinc-100">
          {field.label}
          {field.required ? <span className="ml-1 text-red-300">*</span> : null}
        </legend>
        {field.help ? <p className="text-xs text-zinc-400">{field.help}</p> : null}
        <FieldControl
          field={field}
          controlId={controlId}
          value={currentValue}
          onChange={onChange}
        />
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
          {field.required ? <span className="ml-1 text-red-300">*</span> : null}
        </label>
        {field.help ? <p className="text-xs text-zinc-400">{field.help}</p> : null}
      </div>
      <FieldControl field={field} controlId={controlId} value={currentValue} onChange={onChange} />
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
            <p className="text-xs text-zinc-400">{section.description}</p>
          ) : null}
        </div>
        {visibleRows.map((row, index) => (
          <div
            key={repeatableRowKey(section.id, row)}
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
                key={`${repeatableRowKey(section.id, row)}-${field.id}`}
                field={field}
                controlId={`${section.id}-${field.id}-${repeatableRowKey(section.id, row)}`}
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
          <p className="text-xs text-zinc-400">{section.description}</p>
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
  onChange,
}: {
  field: FormCollectField;
  controlId: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  switch (field.type) {
    case "textarea":
      return (
        <Textarea
          id={controlId}
          value={String(value ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onChange(event.currentTarget.value)}
          data-testid={`form-collect-input-${field.id}`}
        />
      );
    case "select":
      return (
        <select
          id={controlId}
          value={String(value ?? "")}
          onChange={(event) => onChange(event.currentTarget.value)}
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
                data-testid={`form-collect-radio-${field.id}-${option.value}`}
              />
              <label htmlFor={`${controlId}-${option.value}`}>{option.label}</label>
            </div>
          ))}
        </div>
      );
    case "checkbox":
      return (
        <div className="flex items-center gap-2 text-sm text-zinc-200">
          <Checkbox
            id={controlId}
            checked={Boolean(value)}
            onChange={(event) => onChange(event.currentTarget.checked)}
          />
          <label htmlFor={controlId}>{field.placeholder ?? "Checked"}</label>
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
          data-testid={`form-collect-input-${field.id}`}
        />
      );
  }
}

function SavedItemsEditor<
  T extends { id: string; label: string; answers: Record<string, unknown>; notes?: string },
>({
  title,
  inputLabel,
  setInputLabel,
  items,
  onSave,
  onRestore,
}: {
  title: string;
  inputLabel: string;
  setInputLabel: (value: string) => void;
  items: T[];
  onSave: () => void;
  onRestore: (item: T) => void;
}) {
  return (
    <section className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/50 p-4">
      <p className="text-sm font-medium text-zinc-100">{title}</p>
      <div className="flex gap-2">
        <Input value={inputLabel} onChange={(event) => setInputLabel(event.currentTarget.value)} />
        <Button type="button" variant="outline" onClick={onSave}>
          Save
        </Button>
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

function isGroupedFieldType(type: FormCollectFieldType): boolean {
  return type === "multiselect" || type === "radio" || type === "checkbox";
}

function evaluateCompletion(schema: FormCollectSchema, answers: Record<string, unknown>) {
  let visibleCount = 0;
  let requiredRemaining = 0;
  for (const field of schema.fields ?? []) {
    if (!isVisible(field.show_when, answers)) {
      continue;
    }
    visibleCount += 1;
    if (field.required && !isFilled(field, answers[field.id])) {
      requiredRemaining += 1;
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
    for (const row of rows) {
      for (const field of section.fields) {
        if (!isVisible(field.show_when, { ...answers, ...row })) {
          continue;
        }
        visibleCount += 1;
        if (field.required && !isFilled(field, row[field.id])) {
          requiredRemaining += 1;
        }
      }
    }
  }
  return { visibleCount, requiredRemaining };
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

function repeatableRowKey(sectionID: string, row: Record<string, unknown>): string {
  return `${sectionID}-${readRepeatableRowID(row)}`;
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
