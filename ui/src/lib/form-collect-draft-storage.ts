export const FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS = 400;

const FORM_COLLECT_DRAFT_STORAGE_PREFIX = "tangent:form-collect-draft:v1";

export type FormCollectDraftRecord = {
  version: 1;
  roomID: string;
  formID: string;
  envelopeId: string;
  baseSeedKey: string;
  answers: Record<string, unknown>;
  notes: string;
  actionID: string;
  savedDrafts: Array<Record<string, unknown>>;
  templates: Array<Record<string, unknown>>;
  attachmentRefs: Array<Record<string, unknown>>;
  savedAt: string;
};

export function getFormCollectDraftStorageKey(roomID: string, formID: string): string {
  return `${FORM_COLLECT_DRAFT_STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(formID)}`;
}

export function loadFormCollectDraft(
  roomID: string,
  formID: string,
): FormCollectDraftRecord | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getFormCollectDraftStorageKey(roomID, formID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<FormCollectDraftRecord>;
    if (
      parsed.version !== 1 ||
      parsed.roomID !== roomID ||
      parsed.formID !== formID ||
      typeof parsed.envelopeId !== "string" ||
      typeof parsed.baseSeedKey !== "string" ||
      !parsed.answers ||
      typeof parsed.answers !== "object" ||
      typeof parsed.notes !== "string" ||
      typeof parsed.actionID !== "string" ||
      !Array.isArray(parsed.savedDrafts) ||
      !Array.isArray(parsed.templates) ||
      !Array.isArray(parsed.attachmentRefs) ||
      typeof parsed.savedAt !== "string"
    ) {
      storage.removeItem(getFormCollectDraftStorageKey(roomID, formID));
      return null;
    }
    return parsed as FormCollectDraftRecord;
  } catch {
    storage.removeItem(getFormCollectDraftStorageKey(roomID, formID));
    return null;
  }
}

export function saveFormCollectDraft(record: FormCollectDraftRecord): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(
    getFormCollectDraftStorageKey(record.roomID, record.formID),
    JSON.stringify(record),
  );
}

export function clearFormCollectDraft(roomID: string, formID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getFormCollectDraftStorageKey(roomID, formID));
}

export function buildFormCollectCanonicalSeedKey(input: {
  formID: string;
  schema: unknown;
  answers: Record<string, unknown>;
  notes: string;
  templates: unknown[];
}): string {
  return JSON.stringify({
    formID: input.formID,
    schema: input.schema,
    answers: input.answers,
    notes: input.notes,
    templates: input.templates,
  });
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
