const STORAGE_PREFIX = "tangent.file-picker.draft.v1";

export type FilePickerDraft = {
  activeRootID: string;
  currentDir: string;
  search: string;
  sort: string;
  selectedKeys: string[];
  previewKey: string;
};

export function loadFilePickerDraft(roomID: string, pickerID: string): FilePickerDraft | null {
  try {
    const raw = window.localStorage.getItem(storageKey(roomID, pickerID));
    if (!raw) {
      return null;
    }
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
    return null;
  }
}

export function saveFilePickerDraft(
  roomID: string,
  pickerID: string,
  draft: FilePickerDraft,
): void {
  window.localStorage.setItem(storageKey(roomID, pickerID), JSON.stringify(draft));
}

export function clearFilePickerDraft(roomID: string, pickerID: string): void {
  window.localStorage.removeItem(storageKey(roomID, pickerID));
}

function storageKey(roomID: string, pickerID: string): string {
  return `${STORAGE_PREFIX}:${roomID}:${pickerID}`;
}
