export const SPREADSHEET_REVIEW_AUTOSAVE_DEBOUNCE_MS = 400;

const SPREADSHEET_REVIEW_DRAFT_STORAGE_PREFIX = "tangent:spreadsheet-review-draft:v1";

export type SpreadsheetReviewDraftRecord = {
  version: 1;
  roomID: string;
  tableID: string;
  envelopeId: string;
  baseSeedKey: string;
  queryState: Record<string, unknown>;
  selectedRowIDs: string[];
  notes: string;
  actionID: string;
  savedViews: Array<{ name: string; query_state: Record<string, unknown> }>;
  exportRefs: Array<Record<string, unknown>>;
  savedAt: string;
};

export function getSpreadsheetReviewDraftStorageKey(roomID: string, tableID: string): string {
  return `${SPREADSHEET_REVIEW_DRAFT_STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(tableID)}`;
}

export function loadSpreadsheetReviewDraft(
  roomID: string,
  tableID: string,
): SpreadsheetReviewDraftRecord | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getSpreadsheetReviewDraftStorageKey(roomID, tableID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<SpreadsheetReviewDraftRecord>;
    if (
      parsed.version !== 1 ||
      parsed.roomID !== roomID ||
      parsed.tableID !== tableID ||
      typeof parsed.envelopeId !== "string" ||
      typeof parsed.baseSeedKey !== "string" ||
      !parsed.queryState ||
      typeof parsed.queryState !== "object" ||
      !Array.isArray(parsed.selectedRowIDs) ||
      typeof parsed.notes !== "string" ||
      typeof parsed.actionID !== "string" ||
      !Array.isArray(parsed.savedViews) ||
      !Array.isArray(parsed.exportRefs) ||
      typeof parsed.savedAt !== "string"
    ) {
      storage.removeItem(getSpreadsheetReviewDraftStorageKey(roomID, tableID));
      return null;
    }
    return parsed as SpreadsheetReviewDraftRecord;
  } catch {
    storage.removeItem(getSpreadsheetReviewDraftStorageKey(roomID, tableID));
    return null;
  }
}

export function saveSpreadsheetReviewDraft(record: SpreadsheetReviewDraftRecord): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(
    getSpreadsheetReviewDraftStorageKey(record.roomID, record.tableID),
    JSON.stringify(record),
  );
}

export function clearSpreadsheetReviewDraft(roomID: string, tableID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getSpreadsheetReviewDraftStorageKey(roomID, tableID));
}

export function buildSpreadsheetReviewCanonicalSeedKey(input: {
  tableID: string;
  columns: unknown[];
  rows: unknown[];
  queryState: Record<string, unknown>;
  notes: string;
  savedViews: unknown[];
}): string {
  return JSON.stringify({
    tableID: input.tableID,
    columns: input.columns,
    rows: input.rows,
    queryState: input.queryState,
    notes: input.notes,
    savedViews: input.savedViews,
  });
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
