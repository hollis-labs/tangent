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
      !isValidDraftIndex(parsed.currentIndex) ||
      !Array.isArray(parsed.decisions) ||
      !parsed.decisions.every(isValidDraftDecision) ||
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

function isValidDraftIndex(value: unknown): value is number {
  return (
    typeof value === "number" && Number.isFinite(value) && Number.isInteger(value) && value >= 0
  );
}

function isValidDraftDecision(value: unknown): value is ApprovalQueueDraftDecision {
  if (!value || typeof value !== "object") {
    return false;
  }
  const decision = value as Record<string, unknown>;
  return (
    typeof decision.item_id === "string" &&
    decision.item_id.trim().length > 0 &&
    (decision.decision === "accept" ||
      decision.decision === "reject" ||
      decision.decision === "defer") &&
    typeof decision.comment === "string" &&
    typeof decision.action_id === "string" &&
    typeof decision.defer_reason === "string" &&
    (decision.decided_at === undefined || typeof decision.decided_at === "string")
  );
}
