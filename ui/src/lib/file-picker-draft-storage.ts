export const FILE_PICKER_AUTOSAVE_DEBOUNCE_MS = 400;

const STORAGE_PREFIX = "tangent.file-picker.draft.v1";

export type FilePickerDraft = {
  activeRootID: string;
  currentDir: string;
  search: string;
  sort: string;
  selectedKeys: string[];
  previewKey: string;
};

export function getFilePickerDraftStorageKey(roomID: string, pickerID: string): string {
  return `${STORAGE_PREFIX}:${roomID}:${pickerID}`;
}

export function loadFilePickerDraft(roomID: string, pickerID: string): FilePickerDraft | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const key = getFilePickerDraftStorageKey(roomID, pickerID);
  const raw = storage.getItem(key);
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<FilePickerDraft>;
    return {
      activeRootID: typeof parsed.activeRootID === "string" ? parsed.activeRootID : "",
      currentDir: typeof parsed.currentDir === "string" ? parsed.currentDir : "",
      search: typeof parsed.search === "string" ? parsed.search : "",
      sort: typeof parsed.sort === "string" ? parsed.sort : "name:asc",
      selectedKeys: Array.isArray(parsed.selectedKeys)
        ? parsed.selectedKeys.filter((item): item is string => typeof item === "string")
        : [],
      previewKey: typeof parsed.previewKey === "string" ? parsed.previewKey : "",
    };
  } catch {
    storage.removeItem(key);
    return null;
  }
}

export function saveFilePickerDraft(
  roomID: string,
  pickerID: string,
  draft: FilePickerDraft,
): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(getFilePickerDraftStorageKey(roomID, pickerID), JSON.stringify(draft));
}

export function clearFilePickerDraft(roomID: string, pickerID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getFilePickerDraftStorageKey(roomID, pickerID));
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
