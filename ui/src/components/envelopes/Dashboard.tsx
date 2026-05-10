import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
    saved_layouts?: Array<{
      layout_id: string;
      name: string;
      is_default?: boolean;
    }>;
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
  };
  completedAt?: string;
}

type DashboardProps = {
  envelope: DashboardEnvelope;
  onSubmit: (response: DashboardResponse) => void;
  onCancel: () => void;
};

export function Dashboard({ envelope, onSubmit, onCancel }: DashboardProps) {
  const dashboardID = envelope.data?.dashboard_id ?? "";
  const tiles = envelope.data?.tiles ?? [];
  const layout = envelope.data?.layout ?? [];
  const summary = envelope.data?.summary;
  const queryState = envelope.data?.query_state;
  const snapshotHistory = envelope.data?.snapshot_history ?? [];
  const [note, setNote] = useState("");

  const orderedTiles = useMemo(() => {
    if (layout.length === 0) {
      return tiles;
    }
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

  const activeLayout = envelope.data?.saved_layouts?.find(
    (item) => item.layout_id === envelope.data?.active_layout_id,
  );

  const submit = (action: "refresh" | "update") => {
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        dashboard_id: dashboardID,
        action,
        note: note.trim() || undefined,
      },
      completedAt: new Date().toISOString(),
    });
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
            </div>
          ))}
        </section>

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
