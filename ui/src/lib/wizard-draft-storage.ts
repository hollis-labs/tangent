const WIZARD_DRAFT_STORAGE_PREFIX = "tangent:wizard-draft:v1";
export const WIZARD_AUTOSAVE_DEBOUNCE_MS = 400;

export type WizardDraftRecord = {
  version: 1;
  roomID: string;
  wizardID: string;
  envelopeId: string;
  baseSeedKey: string;
  currentStepID: string;
  progress: Array<Record<string, unknown>>;
  branchSelections: Array<Record<string, unknown>>;
  savedAt: string;
};

function storageKey(roomID: string, wizardID: string): string {
  return `${WIZARD_DRAFT_STORAGE_PREFIX}:${roomID}:${wizardID}`;
}

export function buildWizardCanonicalSeedKey(input: {
  wizardID: string;
  stepIDs: string[];
  currentStepID: string;
}): string {
  return JSON.stringify(input);
}

export function loadWizardDraft(roomID: string, wizardID: string): WizardDraftRecord | null {
  try {
    const raw = window.localStorage.getItem(storageKey(roomID, wizardID));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<WizardDraftRecord>;
    if (parsed.version !== 1 || parsed.roomID !== roomID || parsed.wizardID !== wizardID) {
      return null;
    }
    return {
      version: 1,
      roomID,
      wizardID,
      envelopeId: typeof parsed.envelopeId === "string" ? parsed.envelopeId : "",
      baseSeedKey: typeof parsed.baseSeedKey === "string" ? parsed.baseSeedKey : "",
      currentStepID: typeof parsed.currentStepID === "string" ? parsed.currentStepID : "",
      progress: Array.isArray(parsed.progress) ? parsed.progress : [],
      branchSelections: Array.isArray(parsed.branchSelections) ? parsed.branchSelections : [],
      savedAt: typeof parsed.savedAt === "string" ? parsed.savedAt : "",
    };
  } catch {
    return null;
  }
}

export function saveWizardDraft(record: WizardDraftRecord): void {
  try {
    window.localStorage.setItem(storageKey(record.roomID, record.wizardID), JSON.stringify(record));
  } catch {
    // Ignore draft persistence failures in restricted or quota-limited browsers.
  }
}

export function clearWizardDraft(roomID: string, wizardID: string): void {
  try {
    window.localStorage.removeItem(storageKey(roomID, wizardID));
  } catch {
    // Ignore draft cleanup failures in restricted browsers.
  }
}
