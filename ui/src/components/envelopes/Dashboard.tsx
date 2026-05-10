import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  clearDashboardDraft,
  DASHBOARD_DRAFT_AUTOSAVE_DEBOUNCE_MS,
  loadDashboardDraft,
  saveDashboardDraft,
} from "@/lib/dashboard-draft-storage";
import { cn } from "@/lib/utils";

type DashboardTile = {
  tile_id: string;
  kind: string;
  title: string;
  subtitle?: string;
  status?: string;
  value?: string;
  unit?: string;
  summary?: string;
  room_id?: string;
  workflow?: string;
  artifact_ref?: string;
};

type DashboardLayoutItem = {
  tile_id: string;
  x: number;
  y: number;
  w: number;
  h: number;
};

type DashboardSavedLayout = {
  layout_id: string;
  name: string;
  description?: string;
  tiles: DashboardLayoutItem[];
  is_default?: boolean;
  updated_at?: string;
};

type DashboardQueryState = {
  search?: string;
  scope?: string;
  group_by?: string;
  filters?: Array<{
    filter_id: string;
    label?: string;
    operator?: string;
    values?: string[];
  }>;
  sort?: Array<{
    field: string;
    direction?: string;
  }>;
};

type DashboardSummary = {
  headline?: string;
  detail?: string;
  status?: string;
  tile_count?: number;
  active_room_count?: number;
  last_refresh_at?: string;
  accepted_snapshot_id?: string;
  accepted_snapshot_at?: string;
};

export interface DashboardEnvelope {
  v: number;
  id: string;
  type: "tangent.dashboard";
  title?: string;
  context?: string;
  data?: {
    dashboard_id: string;
    title?: string;
    tiles: DashboardTile[];
    layout?: DashboardLayoutItem[];
    saved_layouts?: DashboardSavedLayout[];
    active_layout_id?: string;
    query_state?: DashboardQueryState;
    summary?: DashboardSummary;
    snapshot_history?: Array<{
      snapshot_id: string;
      action: string;
      note?: string;
      created_at?: string;
      tile_count?: number;
      active_layout_id?: string;
    }>;
    updated_at?: string;
  };
}

export interface DashboardResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    dashboard_id: string;
    action: "refresh" | "update";
    note?: string;
    layout?: DashboardLayoutItem[];
    saved_layouts?: DashboardSavedLayout[];
    active_layout_id?: string;
    query_state?: DashboardQueryState;
  };
  completedAt?: string;
}

type DashboardProps = {
  envelope: DashboardEnvelope;
  onSubmit: (response: DashboardResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function Dashboard({ envelope, onSubmit, onCancel, roomID }: DashboardProps) {
  const dashboardID = envelope.data?.dashboard_id ?? "";
  const tiles = envelope.data?.tiles ?? [];
  const summary = envelope.data?.summary;
  const queryState = envelope.data?.query_state;
  const snapshotHistory = envelope.data?.snapshot_history ?? [];
  const exportState = useMemo(() => buildExportState(envelope.data), [envelope.data]);
  const [draft] = useState(() =>
    roomID && dashboardID ? loadDashboardDraft(roomID, dashboardID) : null,
  );
  const [note, setNote] = useState("");
  const [search, setSearch] = useState(queryState?.search ?? "");
  const [scope, setScope] = useState(queryState?.scope ?? "");
  const [groupBy, setGroupBy] = useState(queryState?.group_by ?? "");
  const [statusFilter, setStatusFilter] = useState(
    queryState?.filters?.find((item) => item.filter_id === "status")?.values?.join(", ") ?? "",
  );
  const [sortField, setSortField] = useState(queryState?.sort?.[0]?.field ?? "updated_at");
  const [sortDirection, setSortDirection] = useState(queryState?.sort?.[0]?.direction ?? "desc");
  const [layout, setLayout] = useState<DashboardLayoutItem[]>(() =>
    normalizeLayoutDraft(draft?.layout, envelope.data?.layout, tiles),
  );
  const [savedLayouts, setSavedLayouts] = useState<DashboardSavedLayout[]>(() =>
    normalizeSavedLayoutsDraft(draft?.savedLayouts, envelope.data?.saved_layouts, tiles),
  );
  const [activeLayoutID, setActiveLayoutID] = useState(
    draft?.activeLayoutID || envelope.data?.active_layout_id || "",
  );
  const [layoutName, setLayoutName] = useState(() =>
    readLayoutName(
      draft?.activeLayoutID || envelope.data?.active_layout_id || "",
      draft?.savedLayouts,
      envelope.data?.saved_layouts,
    ),
  );
  const [layoutMessage, setLayoutMessage] = useState<string | null>(
    draft ? "Recovered unsent dashboard layout edits from this browser." : null,
  );
  const [artifactMessage, setArtifactMessage] = useState<string | null>(null);
  const lastSavedDraftRef = useRef<string | null>(draft ? JSON.stringify(draft) : null);

  const orderedTiles = useMemo(() => {
    const order = new Map(layout.map((item, index) => [item.tile_id, index]));
    return [...tiles].sort((left, right) => {
      const leftOrder = order.get(left.tile_id) ?? Number.MAX_SAFE_INTEGER;
      const rightOrder = order.get(right.tile_id) ?? Number.MAX_SAFE_INTEGER;
      if (leftOrder !== rightOrder) {
        return leftOrder - rightOrder;
      }
      return left.title.localeCompare(right.title);
    });
  }, [layout, tiles]);

  const activeLayout = useMemo(
    () => savedLayouts.find((item) => item.layout_id === activeLayoutID) ?? null,
    [activeLayoutID, savedLayouts],
  );

  useEffect(() => {
    if (!roomID || !dashboardID) {
      return;
    }
    const nextDraft = {
      activeLayoutID,
      layout,
      savedLayouts,
    };
    const serialized = JSON.stringify(nextDraft);
    if (serialized === lastSavedDraftRef.current) {
      return;
    }
    const handle = window.setTimeout(() => {
      saveDashboardDraft(roomID, dashboardID, nextDraft);
      lastSavedDraftRef.current = serialized;
    }, DASHBOARD_DRAFT_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(handle);
  }, [activeLayoutID, dashboardID, layout, roomID, savedLayouts]);

  const submit = (action: "refresh" | "update") => {
    if (roomID && dashboardID) {
      clearDashboardDraft(roomID, dashboardID);
      lastSavedDraftRef.current = null;
    }
    const normalizedFilterValues = statusFilter
      .split(",")
      .map((value) => value.trim())
      .filter((value) => value.length > 0);
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        dashboard_id: dashboardID,
        action,
        note: note.trim() || undefined,
        layout,
        saved_layouts: savedLayouts,
        active_layout_id: activeLayoutID || undefined,
        query_state: {
          search: search.trim() || undefined,
          scope: scope || undefined,
          group_by: groupBy.trim() || undefined,
          filters:
            normalizedFilterValues.length > 0
              ? [
                  {
                    filter_id: "status",
                    label: "Status",
                    operator: "in",
                    values: normalizedFilterValues,
                  },
                ]
              : [],
          sort: sortField ? [{ field: sortField, direction: sortDirection || "desc" }] : [],
        },
      },
      completedAt: new Date().toISOString(),
    });
  };

  const selectLayout = (layoutID: string) => {
    setActiveLayoutID(layoutID);
    const next = savedLayouts.find((item) => item.layout_id === layoutID) ?? null;
    setLayout(next ? cloneLayout(next.tiles) : buildDefaultLayout(tiles));
    setLayoutName(next?.name ?? "");
    setLayoutMessage(
      next ? `Loaded saved layout "${next.name}".` : "Switched back to the current session layout.",
    );
  };

  const saveLayout = (mode: "update" | "duplicate") => {
    const trimmedName = layoutName.trim();
    if (!trimmedName) {
      setLayoutMessage("Layout name is required before saving.");
      return;
    }
    const timestamp = new Date().toISOString();
    const baseID =
      mode === "update" && activeLayout ? activeLayout.layout_id : slugifyLayoutName(trimmedName);
    const nextLayoutID =
      mode === "update" && activeLayout
        ? activeLayout.layout_id
        : ensureUniqueLayoutID(savedLayouts, baseID || "layout");
    const nextLayout: DashboardSavedLayout = {
      layout_id: nextLayoutID,
      name: trimmedName,
      description: activeLayout?.description,
      tiles: cloneLayout(layout),
      is_default: activeLayout?.is_default ?? savedLayouts.length === 0,
      updated_at: timestamp,
    };
    const remainder =
      mode === "update"
        ? savedLayouts.filter((item) => item.layout_id !== nextLayoutID)
        : savedLayouts.slice();
    const nextSavedLayouts = normalizeDefaultLayout([...remainder, nextLayout]);
    setSavedLayouts(nextSavedLayouts);
    setActiveLayoutID(nextLayoutID);
    setLayoutName(trimmedName);
    setLayoutMessage(
      mode === "update"
        ? `Saved changes to "${trimmedName}".`
        : `Created saved layout "${trimmedName}".`,
    );
  };

  const moveTile = (tileID: string, direction: -1 | 1) => {
    const currentIndex = layout.findIndex((item) => item.tile_id === tileID);
    const targetIndex = currentIndex + direction;
    if (currentIndex < 0 || targetIndex < 0 || targetIndex >= layout.length) {
      return;
    }
    const nextLayout = cloneLayout(layout);
    const [moved] = nextLayout.splice(currentIndex, 1);
    nextLayout.splice(targetIndex, 0, moved);
    setLayout(normalizeLayoutOrder(nextLayout));
    setLayoutMessage(`Reordered ${readTileTitle(tiles, tileID)}.`);
  };

  const applyLayoutToSaved = () => {
    if (!activeLayout) {
      setLayoutMessage("Save the current arrangement as a named layout first.");
      return;
    }
    setSavedLayouts(
      normalizeDefaultLayout(
        savedLayouts.map((item) =>
          item.layout_id === activeLayout.layout_id
            ? { ...item, tiles: cloneLayout(layout), updated_at: new Date().toISOString() }
            : item,
        ),
      ),
    );
    setLayoutMessage(`Updated tile arrangement for "${activeLayout.name}".`);
  };

  const copyArtifactRef = async (artifactRef: string) => {
    try {
      await navigator.clipboard.writeText(artifactRef);
      setArtifactMessage(`Copied ${artifactRef}.`);
    } catch {
      setArtifactMessage(`Artifact ref: ${artifactRef}`);
    }
  };

  return (
    <Card className="w-full max-w-6xl" data-testid="dashboard-root">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle>{envelope.title ?? envelope.data?.title ?? "Dashboard"}</CardTitle>
          {envelope.context ? <p className="text-sm text-zinc-400">{envelope.context}</p> : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          <span className="rounded-full border border-zinc-800 px-2 py-1">
            dashboard {dashboardID || "unknown"}
          </span>
          {summary?.status ? (
            <span className="rounded-full border border-zinc-800 px-2 py-1">{summary.status}</span>
          ) : null}
          {activeLayout ? (
            <span className="rounded-full border border-zinc-800 px-2 py-1">
              layout {activeLayout.name}
            </span>
          ) : null}
          {summary?.last_refresh_at ? (
            <span className="rounded-full border border-zinc-800 px-2 py-1">
              refreshed {summary.last_refresh_at}
            </span>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        {summary ? (
          <section
            className="rounded-xl border border-zinc-800 bg-zinc-950/50 p-4"
            data-testid="dashboard-summary"
          >
            <p className="text-sm font-medium text-zinc-100">
              {summary.headline || "No summary yet"}
            </p>
            {summary.detail ? <p className="mt-1 text-sm text-zinc-400">{summary.detail}</p> : null}
            <div className="mt-3 flex flex-wrap gap-2 text-xs text-zinc-500">
              <span>{summary.tile_count ?? tiles.length} tiles</span>
              {summary.active_room_count !== undefined ? (
                <span>{summary.active_room_count} active rooms</span>
              ) : null}
              {summary.accepted_snapshot_id ? <span>{summary.accepted_snapshot_id}</span> : null}
            </div>
          </section>
        ) : null}

        {queryState ? (
          <section
            className="flex flex-wrap gap-2 text-xs text-zinc-400"
            data-testid="dashboard-query-state"
          >
            {queryState.scope ? <span>scope: {queryState.scope}</span> : null}
            {queryState.search ? <span>search: {queryState.search}</span> : null}
            {queryState.group_by ? <span>group: {queryState.group_by}</span> : null}
            {(queryState.filters ?? []).map((filter) => (
              <span key={filter.filter_id}>
                {`${filter.label || filter.filter_id} `}
                {`${filter.operator || "in"} `}
                {(filter.values ?? []).join(", ")}
              </span>
            ))}
          </section>
        ) : null}

        <section
          className="grid gap-3 rounded-xl border border-zinc-800 bg-zinc-950/40 p-4 md:grid-cols-2"
          data-testid="dashboard-controls"
        >
          <div className="space-y-2">
            <label
              htmlFor="dashboard-search"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Search
            </label>
            <input
              id="dashboard-search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              data-testid="dashboard-search"
            />
          </div>
          <div className="space-y-2">
            <label
              htmlFor="dashboard-scope"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Scope
            </label>
            <select
              id="dashboard-scope"
              value={scope}
              onChange={(event) => setScope(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              data-testid="dashboard-scope"
            >
              <option value="">Default</option>
              <option value="active">Active</option>
              <option value="all">All rooms</option>
              <option value="attention">Attention only</option>
            </select>
          </div>
          <div className="space-y-2">
            <label
              htmlFor="dashboard-group-by"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Group by
            </label>
            <input
              id="dashboard-group-by"
              value={groupBy}
              onChange={(event) => setGroupBy(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              data-testid="dashboard-group-by"
            />
          </div>
          <div className="space-y-2">
            <label
              htmlFor="dashboard-status-filter"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Status filter
            </label>
            <input
              id="dashboard-status-filter"
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              placeholder="running, blocked"
              data-testid="dashboard-status-filter"
            />
          </div>
          <div className="space-y-2">
            <label
              htmlFor="dashboard-sort-field"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Sort field
            </label>
            <input
              id="dashboard-sort-field"
              value={sortField}
              onChange={(event) => setSortField(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              data-testid="dashboard-sort-field"
            />
          </div>
          <div className="space-y-2">
            <label
              htmlFor="dashboard-sort-direction"
              className="text-xs font-medium uppercase tracking-wide text-zinc-500"
            >
              Sort direction
            </label>
            <select
              id="dashboard-sort-direction"
              value={sortDirection}
              onChange={(event) => setSortDirection(event.target.value)}
              className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
              data-testid="dashboard-sort-direction"
            >
              <option value="desc">Descending</option>
              <option value="asc">Ascending</option>
            </select>
          </div>
        </section>

        <section
          className="grid gap-4 rounded-xl border border-zinc-800 bg-zinc-950/40 p-4 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)]"
          data-testid="dashboard-layouts"
        >
          <div className="space-y-3">
            <div className="space-y-2">
              <label
                htmlFor="dashboard-active-layout"
                className="text-xs font-medium uppercase tracking-wide text-zinc-500"
              >
                Saved layouts
              </label>
              <select
                id="dashboard-active-layout"
                value={activeLayoutID}
                onChange={(event) => selectLayout(event.target.value)}
                className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100"
                data-testid="dashboard-active-layout"
              >
                <option value="">Current session</option>
                {savedLayouts.map((item) => (
                  <option key={item.layout_id} value={item.layout_id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-2">
              <label
                htmlFor="dashboard-layout-name"
                className="text-xs font-medium uppercase tracking-wide text-zinc-500"
              >
                Layout name
              </label>
              <input
                id="dashboard-layout-name"
                value={layoutName}
                onChange={(event) => setLayoutName(event.target.value)}
                className="w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100"
                data-testid="dashboard-layout-name"
                placeholder="Focus, Reviews, Triage"
              />
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => saveLayout("update")}
                data-testid="dashboard-save-layout"
              >
                Save layout
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => saveLayout("duplicate")}
                data-testid="dashboard-save-layout-as-new"
              >
                Save as new
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={applyLayoutToSaved}
                data-testid="dashboard-apply-layout-order"
              >
                Apply order
              </Button>
            </div>
            {layoutMessage ? (
              <p className="text-sm text-emerald-300" data-testid="dashboard-layout-message">
                {layoutMessage}
              </p>
            ) : null}
          </div>

          <div className="space-y-2">
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Tile order</p>
            <div className="space-y-2" data-testid="dashboard-layout-order">
              {orderedTiles.map((tile, index) => (
                <div
                  key={tile.tile_id}
                  className="flex items-center justify-between rounded-xl border border-zinc-800 bg-zinc-950/60 px-3 py-3"
                >
                  <div>
                    <p className="text-sm font-medium text-zinc-100">{tile.title}</p>
                    <p className="text-xs text-zinc-500">{tile.kind}</p>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={index === 0}
                      onClick={() => moveTile(tile.tile_id, -1)}
                      data-testid={`dashboard-move-up-${tile.tile_id}`}
                    >
                      Up
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={index === orderedTiles.length - 1}
                      onClick={() => moveTile(tile.tile_id, 1)}
                      data-testid={`dashboard-move-down-${tile.tile_id}`}
                    >
                      Down
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </section>

        <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="dashboard-tiles">
          {orderedTiles.map((tile) => (
            <div
              key={tile.tile_id}
              className={cn(
                "rounded-xl border border-zinc-800 bg-zinc-950/50 p-4",
                tile.status === "attention" || tile.status === "blocked"
                  ? "border-amber-700/60"
                  : undefined,
              )}
              data-testid={`dashboard-tile-${tile.tile_id}`}
            >
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-zinc-100">{tile.title}</p>
                  {tile.subtitle ? <p className="text-xs text-zinc-500">{tile.subtitle}</p> : null}
                </div>
                {tile.status ? (
                  <span className="rounded-full border border-zinc-700 px-2 py-1 text-[11px] text-zinc-400">
                    {tile.status}
                  </span>
                ) : null}
              </div>
              <p className="mt-4 text-3xl font-semibold tracking-tight text-zinc-50">
                {tile.value || "0"}
                {tile.unit ? <span className="ml-1 text-sm text-zinc-400">{tile.unit}</span> : null}
              </p>
              {tile.summary ? <p className="mt-2 text-sm text-zinc-400">{tile.summary}</p> : null}
              <div className="mt-3 flex flex-wrap gap-2 text-[11px] text-zinc-500">
                {tile.workflow ? <span>{tile.workflow}</span> : null}
                {tile.room_id ? <span>room {tile.room_id}</span> : null}
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                {tile.room_id ? (
                  <a
                    href={`/r/${tile.room_id}`}
                    target="_blank"
                    rel="noreferrer"
                    className="rounded-full border border-zinc-700 px-2 py-1 text-xs text-zinc-200"
                    data-testid={`dashboard-open-room-${tile.tile_id}`}
                  >
                    Open room
                  </a>
                ) : null}
                {tile.artifact_ref ? (
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => {
                      if (tile.artifact_ref) {
                        void copyArtifactRef(tile.artifact_ref);
                      }
                    }}
                    data-testid={`dashboard-copy-artifact-${tile.tile_id}`}
                  >
                    Copy artifact ref
                  </Button>
                ) : null}
              </div>
            </div>
          ))}
        </section>

        {artifactMessage ? (
          <p className="text-sm text-emerald-300" data-testid="dashboard-artifact-message">
            {artifactMessage}
          </p>
        ) : null}

        {snapshotHistory.length > 0 ? (
          <section className="space-y-2" data-testid="dashboard-snapshot-history">
            <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
              Accepted snapshots
            </p>
            <div className="space-y-2">
              {snapshotHistory
                .slice()
                .reverse()
                .map((snapshot) => (
                  <div
                    key={snapshot.snapshot_id}
                    className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2 text-sm text-zinc-300"
                  >
                    <span className="font-medium text-zinc-100">{snapshot.snapshot_id}</span>
                    <span className="ml-2 text-zinc-500">{snapshot.action}</span>
                    {snapshot.created_at ? (
                      <span className="ml-2 text-zinc-500">{snapshot.created_at}</span>
                    ) : null}
                    {snapshot.note ? <p className="mt-1 text-zinc-400">{snapshot.note}</p> : null}
                  </div>
                ))}
            </div>
          </section>
        ) : null}

        <section
          className="rounded-xl border border-zinc-800 bg-zinc-950/40 p-4"
          data-testid="dashboard-export-state"
        >
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
            Export / share surface
          </p>
          <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words text-xs text-zinc-300">
            {JSON.stringify(exportState, null, 2)}
          </pre>
        </section>

        <section className="space-y-2">
          <label
            htmlFor="dashboard-note"
            className="text-xs font-medium uppercase tracking-wide text-zinc-500"
          >
            Operator note
          </label>
          <textarea
            id="dashboard-note"
            value={note}
            onChange={(event) => setNote(event.target.value)}
            className="min-h-24 w-full rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none transition focus:border-zinc-600"
            placeholder="Capture what changed or what should refresh."
            data-testid="dashboard-note"
          />
        </section>

        <div className="flex flex-wrap justify-end gap-3">
          <Button type="button" variant="ghost" onClick={onCancel} data-testid="dashboard-cancel">
            Cancel
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => submit("refresh")}
            data-testid="dashboard-refresh"
          >
            Refresh
          </Button>
          <Button type="button" onClick={() => submit("update")} data-testid="dashboard-update">
            Submit update
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function normalizeLayoutDraft(
  draftLayout: DashboardLayoutItem[] | undefined,
  envelopeLayout: DashboardLayoutItem[] | undefined,
  tiles: DashboardTile[],
): DashboardLayoutItem[] {
  if (draftLayout && draftLayout.length > 0) {
    return normalizeLayoutOrder(filterKnownTiles(draftLayout, tiles));
  }
  if (envelopeLayout && envelopeLayout.length > 0) {
    return normalizeLayoutOrder(filterKnownTiles(envelopeLayout, tiles));
  }
  return buildDefaultLayout(tiles);
}

function normalizeSavedLayoutsDraft(
  draftLayouts: DashboardSavedLayout[] | undefined,
  envelopeLayouts: DashboardSavedLayout[] | undefined,
  tiles: DashboardTile[],
): DashboardSavedLayout[] {
  const source = draftLayouts && draftLayouts.length > 0 ? draftLayouts : (envelopeLayouts ?? []);
  return normalizeDefaultLayout(
    source.map((item) => ({
      ...item,
      tiles: normalizeLayoutOrder(filterKnownTiles(item.tiles ?? [], tiles)),
    })),
  );
}

function normalizeDefaultLayout(items: DashboardSavedLayout[]): DashboardSavedLayout[] {
  let defaultAssigned = false;
  return items.map((item, index) => {
    const isDefault = item.is_default === true || (!defaultAssigned && index === 0);
    if (isDefault) {
      defaultAssigned = true;
    }
    return { ...item, is_default: isDefault };
  });
}

function filterKnownTiles(
  layout: DashboardLayoutItem[],
  tiles: DashboardTile[],
): DashboardLayoutItem[] {
  const known = new Set(tiles.map((tile) => tile.tile_id));
  const filtered = layout.filter((item) => known.has(item.tile_id));
  const missing = tiles
    .filter((tile) => !filtered.some((item) => item.tile_id === tile.tile_id))
    .map((tile, index) => ({
      tile_id: tile.tile_id,
      x: filtered.length + index,
      y: 0,
      w: 1,
      h: 1,
    }));
  return [...filtered, ...missing];
}

function buildDefaultLayout(tiles: DashboardTile[]): DashboardLayoutItem[] {
  return tiles.map((tile, index) => ({
    tile_id: tile.tile_id,
    x: index,
    y: 0,
    w: 1,
    h: 1,
  }));
}

function normalizeLayoutOrder(layout: DashboardLayoutItem[]): DashboardLayoutItem[] {
  return layout.map((item, index) => ({
    ...item,
    x: index,
    y: 0,
  }));
}

function cloneLayout(layout: DashboardLayoutItem[]): DashboardLayoutItem[] {
  return layout.map((item) => ({ ...item }));
}

function readTileTitle(tiles: DashboardTile[], tileID: string): string {
  return tiles.find((item) => item.tile_id === tileID)?.title ?? tileID;
}

function slugifyLayoutName(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function ensureUniqueLayoutID(savedLayouts: DashboardSavedLayout[], baseID: string): string {
  const existing = new Set(savedLayouts.map((item) => item.layout_id));
  if (!existing.has(baseID)) {
    return baseID;
  }
  let suffix = 2;
  while (existing.has(`${baseID}-${suffix}`)) {
    suffix += 1;
  }
  return `${baseID}-${suffix}`;
}

function readLayoutName(
  activeLayoutID: string,
  draftLayouts: DashboardSavedLayout[] | undefined,
  envelopeLayouts: DashboardSavedLayout[] | undefined,
): string {
  const combined = draftLayouts && draftLayouts.length > 0 ? draftLayouts : (envelopeLayouts ?? []);
  return combined.find((item) => item.layout_id === activeLayoutID)?.name ?? "";
}

function buildExportState(data: DashboardEnvelope["data"]) {
  const tiles = data?.tiles ?? [];
  return {
    dashboard_id: data?.dashboard_id ?? "",
    active_layout_id: data?.active_layout_id ?? "",
    accepted_snapshot_id: data?.summary?.accepted_snapshot_id ?? "",
    accepted_snapshot_at: data?.summary?.accepted_snapshot_at ?? "",
    room_refs: tiles.map((tile) => tile.room_id).filter(Boolean),
    artifact_refs: tiles.map((tile) => tile.artifact_ref).filter(Boolean),
  };
}
