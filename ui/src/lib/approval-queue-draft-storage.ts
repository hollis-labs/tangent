export const APPROVAL_QUEUE_AUTOSAVE_DEBOUNCE_MS = 400;

const APPROVAL_QUEUE_DRAFT_STORAGE_PREFIX = "tangent:approval-queue-draft:v1";

export type ApprovalQueueDraftDecision = {
  item_id: string;
  decision: "accept" | "reject" | "defer";
  comment: string;
  action_id: string;
  defer_reason: string;
  decided_at?: string;
};

export type ApprovalQueueDraftRecord = {
  version: 1;
  roomID: string;
  queueID: string;
  envelopeId: string;
  baseSeedKey: string;
  currentIndex: number;
  decisions: ApprovalQueueDraftDecision[];
  notes: string;
  exportRefs: Array<Record<string, unknown>>;
  savedAt: string;
};

export function getApprovalQueueDraftStorageKey(roomID: string, queueID: string): string {
  return `${APPROVAL_QUEUE_DRAFT_STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(queueID)}`;
}

export function loadApprovalQueueDraft(
  roomID: string,
  queueID: string,
): ApprovalQueueDraftRecord | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getApprovalQueueDraftStorageKey(roomID, queueID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<ApprovalQueueDraftRecord>;
    if (
      parsed.version !== 1 ||
      parsed.roomID !== roomID ||
      parsed.queueID !== queueID ||
      typeof parsed.envelopeId !== "string" ||
      typeof parsed.baseSeedKey !== "string" ||
      typeof parsed.currentIndex !== "number" ||
      !Array.isArray(parsed.decisions) ||
      typeof parsed.notes !== "string" ||
      !Array.isArray(parsed.exportRefs) ||
      typeof parsed.savedAt !== "string"
    ) {
      storage.removeItem(getApprovalQueueDraftStorageKey(roomID, queueID));
      return null;
    }
    return parsed as ApprovalQueueDraftRecord;
  } catch {
    storage.removeItem(getApprovalQueueDraftStorageKey(roomID, queueID));
    return null;
  }
}

export function saveApprovalQueueDraft(record: ApprovalQueueDraftRecord): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(
    getApprovalQueueDraftStorageKey(record.roomID, record.queueID),
    JSON.stringify(record),
  );
}

export function clearApprovalQueueDraft(roomID: string, queueID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getApprovalQueueDraftStorageKey(roomID, queueID));
}

export function buildApprovalQueueCanonicalSeedKey(input: {
  queueID: string;
  items: unknown[];
  notes: string;
}): string {
  return JSON.stringify(input);
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
