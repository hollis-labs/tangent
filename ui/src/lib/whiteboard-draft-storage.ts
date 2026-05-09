import type { TLEditorSnapshot } from "tldraw";

export const WHITEBOARD_AUTOSAVE_DEBOUNCE_MS = 600;

const WHITEBOARD_DRAFT_STORAGE_PREFIX = "tangent:whiteboard-draft:v1";

export type WhiteboardDraftRecord = {
  version: 1;
  roomID: string;
  boardID: string;
  envelopeId: string;
  baseRevisionId: string | null;
  baseSeedKey: string;
  notes: string;
  scene: TLEditorSnapshot;
  savedAt: string;
};

export function getWhiteboardDraftStorageKey(roomID: string, boardID: string): string {
  return `${WHITEBOARD_DRAFT_STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(boardID)}`;
}

export function loadWhiteboardDraft(roomID: string, boardID: string): WhiteboardDraftRecord | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getWhiteboardDraftStorageKey(roomID, boardID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<WhiteboardDraftRecord>;
    if (
      parsed.version !== 1 ||
      parsed.roomID !== roomID ||
      parsed.boardID !== boardID ||
      typeof parsed.envelopeId !== "string" ||
      (parsed.baseRevisionId !== null && typeof parsed.baseRevisionId !== "string") ||
      typeof parsed.baseSeedKey !== "string" ||
      typeof parsed.notes !== "string" ||
      !parsed.scene ||
      typeof parsed.scene !== "object" ||
      typeof parsed.savedAt !== "string"
    ) {
      storage.removeItem(getWhiteboardDraftStorageKey(roomID, boardID));
      return null;
    }
    return parsed as WhiteboardDraftRecord;
  } catch {
    storage.removeItem(getWhiteboardDraftStorageKey(roomID, boardID));
    return null;
  }
}

export function saveWhiteboardDraft(record: WhiteboardDraftRecord): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(
    getWhiteboardDraftStorageKey(record.roomID, record.boardID),
    JSON.stringify(record),
  );
}

export function clearWhiteboardDraft(roomID: string, boardID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getWhiteboardDraftStorageKey(roomID, boardID));
}

export function buildWhiteboardCanonicalSeedKey(input: {
  notes: string;
  scene?: TLEditorSnapshot;
}): string {
  return JSON.stringify({
    notes: input.notes,
    scene: input.scene ?? null,
  });
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
