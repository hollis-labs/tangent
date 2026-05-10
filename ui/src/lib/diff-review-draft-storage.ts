const STORAGE_PREFIX = "tangent.diff-review.draft.v1";

export const DIFF_REVIEW_AUTOSAVE_DEBOUNCE_MS = 250;

export type DiffReviewDraftDecision = {
  file_id: string;
  hunk_id?: string;
  decision: "accept" | "reject" | "comment";
  comment?: string;
  action_id?: string;
  decided_at?: string;
};

export type DiffReviewDraft = {
  version: 1;
  roomID: string;
  reviewID: string;
  envelopeId: string;
  baseSeedKey: string;
  currentFile: string;
  filterState: Record<string, unknown>;
  decisions: DiffReviewDraftDecision[];
  comments: Record<string, string>;
  exportRefs: Array<Record<string, unknown>>;
  savedAt: string;
};

export function getDiffReviewDraftStorageKey(roomID: string, reviewID: string): string {
  return `${STORAGE_PREFIX}:${roomID}:${reviewID}`;
}

export function saveDiffReviewDraft(draft: DiffReviewDraft): void {
  window.localStorage.setItem(
    getDiffReviewDraftStorageKey(draft.roomID, draft.reviewID),
    JSON.stringify(draft),
  );
}

export function loadDiffReviewDraft(roomID: string, reviewID: string): DiffReviewDraft | null {
  const raw = window.localStorage.getItem(getDiffReviewDraftStorageKey(roomID, reviewID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as DiffReviewDraft;
    if (parsed?.version !== 1 || parsed?.roomID !== roomID || parsed?.reviewID !== reviewID) {
      clearDiffReviewDraft(roomID, reviewID);
      return null;
    }
    return parsed;
  } catch {
    clearDiffReviewDraft(roomID, reviewID);
    return null;
  }
}

export function clearDiffReviewDraft(roomID: string, reviewID: string): void {
  window.localStorage.removeItem(getDiffReviewDraftStorageKey(roomID, reviewID));
}

export function buildDiffReviewCanonicalSeedKey(value: Record<string, unknown>): string {
  return JSON.stringify(value);
}
