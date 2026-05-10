export const PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS = 250;

const STORAGE_PREFIX = "tangent.progress-panel.view.v1";

export type ProgressPanelViewDraft = {
  activeTab: "timeline" | "checkpoints" | "logs";
  filterItemID: string;
  filterKind: string;
  selectedUpdateID: string;
};

export function getProgressPanelDraftStorageKey(roomID: string, panelID: string): string {
  return `${STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(panelID)}`;
}

export function loadProgressPanelDraft(
  roomID: string,
  panelID: string,
): ProgressPanelViewDraft | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getProgressPanelDraftStorageKey(roomID, panelID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<ProgressPanelViewDraft>;
    const activeTab =
      parsed.activeTab === "timeline" ||
      parsed.activeTab === "checkpoints" ||
      parsed.activeTab === "logs"
        ? parsed.activeTab
        : "timeline";
    return {
      activeTab,
      filterItemID: typeof parsed.filterItemID === "string" ? parsed.filterItemID : "",
      filterKind: typeof parsed.filterKind === "string" ? parsed.filterKind : "all",
      selectedUpdateID: typeof parsed.selectedUpdateID === "string" ? parsed.selectedUpdateID : "",
    };
  } catch {
    storage.removeItem(getProgressPanelDraftStorageKey(roomID, panelID));
    return null;
  }
}

export function saveProgressPanelDraft(
  roomID: string,
  panelID: string,
  draft: ProgressPanelViewDraft,
): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(getProgressPanelDraftStorageKey(roomID, panelID), JSON.stringify(draft));
}

export function clearProgressPanelDraft(roomID: string, panelID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getProgressPanelDraftStorageKey(roomID, panelID));
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
