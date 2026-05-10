export const DASHBOARD_DRAFT_AUTOSAVE_DEBOUNCE_MS = 300;

const STORAGE_PREFIX = "tangent.dashboard.draft.v1";

export type DashboardLayoutItemDraft = {
  tile_id: string;
  x: number;
  y: number;
  w: number;
  h: number;
};

export type DashboardSavedLayoutDraft = {
  layout_id: string;
  name: string;
  description?: string;
  tiles: DashboardLayoutItemDraft[];
  is_default?: boolean;
  updated_at?: string;
};

export type DashboardDraft = {
  activeLayoutID: string;
  layout: DashboardLayoutItemDraft[];
  savedLayouts: DashboardSavedLayoutDraft[];
};

export function getDashboardDraftStorageKey(roomID: string, dashboardID: string): string {
  return `${STORAGE_PREFIX}:${encodeURIComponent(roomID)}:${encodeURIComponent(dashboardID)}`;
}

export function loadDashboardDraft(roomID: string, dashboardID: string): DashboardDraft | null {
  const storage = getStorage();
  if (!storage) {
    return null;
  }
  const raw = storage.getItem(getDashboardDraftStorageKey(roomID, dashboardID));
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<DashboardDraft>;
    return {
      activeLayoutID: typeof parsed.activeLayoutID === "string" ? parsed.activeLayoutID : "",
      layout: normalizeLayout(parsed.layout),
      savedLayouts: normalizeSavedLayouts(parsed.savedLayouts),
    };
  } catch {
    storage.removeItem(getDashboardDraftStorageKey(roomID, dashboardID));
    return null;
  }
}

export function saveDashboardDraft(
  roomID: string,
  dashboardID: string,
  draft: DashboardDraft,
): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.setItem(getDashboardDraftStorageKey(roomID, dashboardID), JSON.stringify(draft));
}

export function clearDashboardDraft(roomID: string, dashboardID: string): void {
  const storage = getStorage();
  if (!storage) {
    return;
  }
  storage.removeItem(getDashboardDraftStorageKey(roomID, dashboardID));
}

function normalizeLayout(raw: unknown): DashboardLayoutItemDraft[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.flatMap((item) => {
    if (!item || typeof item !== "object") {
      return [];
    }
    const typed = item as Record<string, unknown>;
    return [
      {
        tile_id: typeof typed.tile_id === "string" ? typed.tile_id : "",
        x: readNumber(typed.x),
        y: readNumber(typed.y),
        w: Math.max(1, readNumber(typed.w, 1)),
        h: Math.max(1, readNumber(typed.h, 1)),
      },
    ].filter((entry) => entry.tile_id);
  });
}

function normalizeSavedLayouts(raw: unknown): DashboardSavedLayoutDraft[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.flatMap((item) => {
    if (!item || typeof item !== "object") {
      return [];
    }
    const typed = item as Record<string, unknown>;
    const layoutID = typeof typed.layout_id === "string" ? typed.layout_id : "";
    const name = typeof typed.name === "string" ? typed.name : "";
    if (!layoutID || !name) {
      return [];
    }
    return [
      {
        layout_id: layoutID,
        name,
        description: typeof typed.description === "string" ? typed.description : undefined,
        tiles: normalizeLayout(typed.tiles),
        is_default: typed.is_default === true,
        updated_at: typeof typed.updated_at === "string" ? typed.updated_at : undefined,
      },
    ];
  });
}

function readNumber(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function getStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}
