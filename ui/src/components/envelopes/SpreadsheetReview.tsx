import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  buildSpreadsheetReviewCanonicalSeedKey,
  clearSpreadsheetReviewDraft,
  loadSpreadsheetReviewDraft,
  SPREADSHEET_REVIEW_AUTOSAVE_DEBOUNCE_MS,
  saveSpreadsheetReviewDraft,
} from "@/lib/spreadsheet-review-draft-storage";
import { cn } from "@/lib/utils";

type QueryFilter = {
  column_id: string;
  op: "contains" | "eq";
  value: string;
};

type QuerySort = {
  column_id: string;
  direction: "asc" | "desc";
};

type QueryState = {
  sort?: QuerySort[];
  filters?: QueryFilter[];
  search?: string;
  visible_columns?: string[];
  page?: { index?: number; size?: number };
};

type Column = {
  id: string;
  label: string;
  sortable?: boolean;
};

type Row = Record<string, unknown> & { id: string };

type RowAction = {
  id: string;
  label: string;
  description?: string;
};

type SavedView = {
  name: string;
  query_state: QueryState;
};

type ExportRef = {
  name: string;
  mime_type?: string;
  kind?: string;
  created_at?: string;
  row_count?: number;
  column_count?: number;
  size_bytes?: number;
};

export interface SpreadsheetReviewEnvelope {
  v: number;
  id: string;
  type: "tangent.spreadsheet-review";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    table_id: string;
    title?: string;
    intent?: string;
    columns?: Array<Record<string, unknown>>;
    rows?: Array<Record<string, unknown>>;
    query_state?: QueryState;
    notes?: string;
    row_actions?: RowAction[];
    selected_row_ids?: string[];
    selected_rows?: Array<Record<string, unknown>>;
    saved_views?: SavedView[];
    action_id?: string;
    export_refs?: ExportRef[];
  };
  meta?: Record<string, unknown>;
}

export interface SpreadsheetReviewResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    table_id: string;
    selected_row_ids: string[];
    selected_rows: Array<Record<string, unknown>>;
    query_state: QueryState;
    notes: string;
    action_id?: string;
    saved_views?: SavedView[];
    export_refs?: ExportRef[];
  };
  completedAt?: string;
}

export type SpreadsheetReviewProps = {
  envelope: SpreadsheetReviewEnvelope;
  onSubmit: (response: SpreadsheetReviewResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function SpreadsheetReview({
  envelope,
  onSubmit,
  onCancel,
  roomID,
}: SpreadsheetReviewProps) {
  const tableID = envelope.data?.table_id ?? "";
  const columns = useMemo(() => normalizeColumns(envelope.data?.columns), [envelope.data?.columns]);
  const rows = useMemo(() => normalizeRows(envelope.data?.rows), [envelope.data?.rows]);
  const rowActions = useMemo(
    () => normalizeRowActions(envelope.data?.row_actions),
    [envelope.data?.row_actions],
  );
  const canonicalQueryState = useMemo(
    () => normalizeQueryState(envelope.data?.query_state, columns),
    [envelope.data?.query_state, columns],
  );
  const canonicalSavedViews = useMemo(
    () => normalizeSavedViews(envelope.data?.saved_views),
    [envelope.data?.saved_views],
  );
  const baseSeedKey = useMemo(
    () =>
      buildSpreadsheetReviewCanonicalSeedKey({
        tableID,
        columns,
        rows,
        queryState: canonicalQueryState,
        notes: envelope.data?.notes ?? "",
        savedViews: canonicalSavedViews,
      }),
    [tableID, columns, rows, canonicalQueryState, envelope.data?.notes, canonicalSavedViews],
  );
  const draft = roomID && tableID ? loadSpreadsheetReviewDraft(roomID, tableID) : null;
  const recoveredDraft =
    draft && (draft.envelopeId === envelope.id || draft.baseSeedKey === baseSeedKey) ? draft : null;

  const [queryState, setQueryState] = useState<QueryState>(() =>
    recoveredDraft
      ? normalizeQueryState(recoveredDraft.queryState as QueryState, columns)
      : canonicalQueryState,
  );
  const [selectedRowIDs, setSelectedRowIDs] = useState<string[]>(() =>
    recoveredDraft
      ? normalizeStringArray(recoveredDraft.selectedRowIDs)
      : normalizeStringArray(envelope.data?.selected_row_ids),
  );
  const [notes, setNotes] = useState<string>(() =>
    recoveredDraft ? recoveredDraft.notes : (envelope.data?.notes ?? ""),
  );
  const [actionID, setActionID] = useState<string>(() =>
    recoveredDraft ? recoveredDraft.actionID : (envelope.data?.action_id ?? ""),
  );
  const [savedViews, setSavedViews] = useState<SavedView[]>(() =>
    recoveredDraft ? normalizeSavedViews(recoveredDraft.savedViews) : canonicalSavedViews,
  );
  const [exportRefs, setExportRefs] = useState<ExportRef[]>(() =>
    recoveredDraft
      ? normalizeExportRefs(recoveredDraft.exportRefs)
      : normalizeExportRefs(envelope.data?.export_refs),
  );
  const [saveViewName, setSaveViewName] = useState("");
  const [filterDraft, setFilterDraft] = useState<QueryFilter>({
    column_id: columns[0]?.id ?? "",
    op: "contains",
    value: "",
  });
  const [message, setMessage] = useState<string | null>(
    recoveredDraft ? "Recovered unsent spreadsheet-review state from this browser." : null,
  );
  const draftJsonRef = useRef<string | null>(null);

  useEffect(() => {
    setFilterDraft((current) => ({
      column_id: current.column_id || columns[0]?.id || "",
      op: current.op,
      value: current.value,
    }));
  }, [columns]);

  const visibleColumnIDs = queryState.visible_columns?.length
    ? queryState.visible_columns
    : columns.map((column) => column.id);
  const visibleColumns = columns.filter((column) => visibleColumnIDs.includes(column.id));
  const filteredRows = useMemo(
    () => applyQueryState(rows, visibleColumnIDs, queryState),
    [rows, visibleColumnIDs, queryState],
  );
  const selectedRows = useMemo(
    () =>
      filteredRows
        .filter((row) => selectedRowIDs.includes(row.id))
        .slice(0, 20)
        .map((row) => compactRow(row, visibleColumnIDs)),
    [filteredRows, selectedRowIDs, visibleColumnIDs],
  );

  const clearDraft = () => {
    if (!roomID || !tableID) {
      return;
    }
    clearSpreadsheetReviewDraft(roomID, tableID);
    draftJsonRef.current = null;
  };

  useEffect(() => {
    if (!roomID || !tableID) {
      return;
    }
    const timerID = window.setTimeout(() => {
      const next = JSON.stringify({
        queryState,
        selectedRowIDs,
        notes,
        actionID,
        savedViews,
        exportRefs,
      });
      if (next === draftJsonRef.current) {
        return;
      }
      const canonical = JSON.stringify({
        queryState: canonicalQueryState,
        selectedRowIDs: normalizeStringArray(envelope.data?.selected_row_ids),
        notes: envelope.data?.notes ?? "",
        actionID: envelope.data?.action_id ?? "",
        savedViews: canonicalSavedViews,
        exportRefs: normalizeExportRefs(envelope.data?.export_refs),
      });
      if (next === canonical) {
        clearSpreadsheetReviewDraft(roomID, tableID);
        draftJsonRef.current = null;
        return;
      }
      saveSpreadsheetReviewDraft({
        version: 1,
        roomID,
        tableID,
        envelopeId: envelope.id,
        baseSeedKey,
        queryState,
        selectedRowIDs,
        notes,
        actionID,
        savedViews,
        exportRefs,
        savedAt: new Date().toISOString(),
      });
      draftJsonRef.current = next;
    }, SPREADSHEET_REVIEW_AUTOSAVE_DEBOUNCE_MS);

    return () => {
      window.clearTimeout(timerID);
    };
  }, [
    roomID,
    tableID,
    envelope.id,
    baseSeedKey,
    queryState,
    selectedRowIDs,
    notes,
    actionID,
    savedViews,
    exportRefs,
    canonicalQueryState,
    envelope.data?.selected_row_ids,
    envelope.data?.notes,
    envelope.data?.action_id,
    envelope.data?.export_refs,
    canonicalSavedViews,
  ]);

  const handleToggleRow = (rowID: string, checked: boolean) => {
    setSelectedRowIDs((current) => {
      if (checked) {
        return current.includes(rowID) ? current : [...current, rowID];
      }
      return current.filter((id) => id !== rowID);
    });
  };

  const handleToggleColumn = (columnID: string, checked: boolean) => {
    setQueryState((current) => {
      const nextVisible = checked
        ? Array.from(new Set([...(current.visible_columns ?? visibleColumnIDs), columnID]))
        : (current.visible_columns ?? visibleColumnIDs).filter((id) => id !== columnID);
      return normalizeQueryState({ ...current, visible_columns: nextVisible }, columns);
    });
  };

  const handleSortToggle = (columnID: string) => {
    setQueryState((current) => {
      const active = current.sort?.[0];
      let nextSort: QuerySort[] | undefined;
      if (!active || active.column_id !== columnID) {
        nextSort = [{ column_id: columnID, direction: "asc" }];
      } else if (active.direction === "asc") {
        nextSort = [{ column_id: columnID, direction: "desc" }];
      } else {
        nextSort = [];
      }
      return normalizeQueryState({ ...current, sort: nextSort }, columns);
    });
  };

  const handleAddFilter = () => {
    if (!filterDraft.column_id || !filterDraft.value.trim()) {
      return;
    }
    setQueryState((current) => {
      const nextFilter = { ...filterDraft, value: filterDraft.value.trim() };
      const nextFilters = [...(current.filters ?? [])];
      const key = getFilterKey(nextFilter);
      if (!nextFilters.some((filter) => getFilterKey(filter) === key)) {
        nextFilters.push(nextFilter);
      }
      return normalizeQueryState(
        {
          ...current,
          filters: nextFilters,
        },
        columns,
      );
    });
    setFilterDraft((current) => ({ ...current, value: "" }));
  };

  const handleRemoveFilter = (filterKey: string) => {
    setQueryState((current) =>
      normalizeQueryState(
        {
          ...current,
          filters: (current.filters ?? []).filter((filter) => getFilterKey(filter) !== filterKey),
        },
        columns,
      ),
    );
  };

  const handleSaveView = () => {
    const name = saveViewName.trim();
    if (!name) {
      return;
    }
    setSavedViews((current) => {
      const next = current.filter((view) => view.name !== name);
      next.push({ name, query_state: queryState });
      return next;
    });
    setSaveViewName("");
    setMessage(`Saved view "${name}".`);
  };

  const handleRestoreView = (view: SavedView) => {
    setQueryState(normalizeQueryState(view.query_state, columns));
    setMessage(`Restored view "${view.name}".`);
  };

  const handleExport = async () => {
    const exportColumns = visibleColumns.length > 0 ? visibleColumns : columns;
    const csv = buildCSV(exportColumns, filteredRows);
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const createdAt = new Date().toISOString();
    const name = `${sanitizeFilePart(tableID || "spreadsheet-review")}-${createdAt.replaceAll(/[:.]/g, "-")}.csv`;
    const url = window.URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = name;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    window.URL.revokeObjectURL(url);
    const nextRef: ExportRef = {
      name,
      mime_type: "text/csv",
      kind: "csv",
      created_at: createdAt,
      row_count: filteredRows.length,
      column_count: exportColumns.length,
      size_bytes: blob.size,
    };
    setExportRefs((current) => [...current, nextRef]);
    setMessage(
      `Exported ${filteredRows.length} row${filteredRows.length === 1 ? "" : "s"} as CSV.`,
    );
  };

  const handleSubmit = () => {
    clearDraft();
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        table_id: tableID,
        selected_row_ids: selectedRowIDs,
        selected_rows: selectedRows,
        query_state: queryState,
        notes,
        action_id: actionID || undefined,
        saved_views: savedViews,
        export_refs: exportRefs,
      },
      completedAt: new Date().toISOString(),
    });
  };

  const handleCancel = () => {
    clearDraft();
    onCancel();
  };

  return (
    <Card className="w-full max-w-[90rem]" data-testid="spreadsheet-review-root">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">
            {envelope.title ?? envelope.data?.title ?? "Spreadsheet review"}
          </CardTitle>
          {envelope.context ? (
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
          {envelope.data?.intent ? (
            <p className="text-sm text-zinc-300">{envelope.data.intent}</p>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2 text-xs text-zinc-400">
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            table {tableID || "unknown"}
          </span>
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            {rows.length} row{rows.length === 1 ? "" : "s"}
          </span>
          <span className="rounded-full border border-zinc-700 px-2 py-1">
            {selectedRowIDs.length} selected
          </span>
          {exportRefs.length > 0 ? (
            <span className="rounded-full border border-zinc-700 px-2 py-1">
              {exportRefs.length} export{exportRefs.length === 1 ? "" : "s"}
            </span>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {message ? (
          <div
            className="rounded-lg border border-zinc-700 bg-zinc-950/70 px-3 py-2 text-sm text-zinc-200"
            data-testid="spreadsheet-review-message"
          >
            {message}
          </div>
        ) : null}

        <section className="grid gap-3 lg:grid-cols-[2fr,1fr,1fr]">
          <Input
            value={queryState.search ?? ""}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setQueryState((current) =>
                normalizeQueryState({ ...current, search: value }, columns),
              );
            }}
            placeholder="Search rows"
            data-testid="spreadsheet-review-search"
          />
          <div className="flex gap-2">
            <select
              className="h-10 w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 text-sm"
              value={filterDraft.column_id}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setFilterDraft((current) => ({ ...current, column_id: value }));
              }}
              data-testid="spreadsheet-review-filter-column"
            >
              {columns.map((column) => (
                <option key={column.id} value={column.id}>
                  {column.label}
                </option>
              ))}
            </select>
            <select
              className="h-10 rounded-md border border-zinc-700 bg-zinc-950 px-3 text-sm"
              value={filterDraft.op}
              onChange={(event) => {
                const value = event.currentTarget.value as QueryFilter["op"];
                setFilterDraft((current) => ({
                  ...current,
                  op: value,
                }));
              }}
              data-testid="spreadsheet-review-filter-op"
            >
              <option value="contains">contains</option>
              <option value="eq">equals</option>
            </select>
          </div>
          <div className="flex gap-2">
            <Input
              value={filterDraft.value}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setFilterDraft((current) => ({ ...current, value }));
              }}
              placeholder="Filter value"
              data-testid="spreadsheet-review-filter-value"
            />
            <Button
              type="button"
              variant="outline"
              onClick={handleAddFilter}
              data-testid="spreadsheet-review-add-filter"
            >
              Add
            </Button>
          </div>
        </section>

        <section className="space-y-2">
          <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
            Visible columns
          </p>
          <div className="flex flex-wrap gap-3">
            {columns.map((column) => (
              <div key={column.id} className="flex items-center gap-2 text-sm text-zinc-300">
                <Checkbox
                  id={`spreadsheet-review-column-${column.id}`}
                  checked={visibleColumnIDs.includes(column.id)}
                  onChange={(event) => handleToggleColumn(column.id, event.currentTarget.checked)}
                />
                <label htmlFor={`spreadsheet-review-column-${column.id}`}>{column.label}</label>
              </div>
            ))}
          </div>
        </section>

        {(queryState.filters ?? []).length > 0 ? (
          <section className="flex flex-wrap gap-2" data-testid="spreadsheet-review-active-filters">
            {(queryState.filters ?? []).map((filter) => (
              <button
                key={getFilterKey(filter)}
                type="button"
                className="rounded-full border border-zinc-700 px-3 py-1 text-xs text-zinc-300"
                onClick={() => handleRemoveFilter(getFilterKey(filter))}
              >
                {filter.column_id} {filter.op} {filter.value} ×
              </button>
            ))}
          </section>
        ) : null}

        <section className="grid gap-4 lg:grid-cols-[2fr,1fr]">
          <div className="overflow-x-auto rounded-xl border border-zinc-800">
            <table className="min-w-full divide-y divide-zinc-800 text-sm">
              <thead className="bg-zinc-950/80">
                <tr>
                  <th className="px-3 py-2 text-left">Select</th>
                  {visibleColumns.map((column) => {
                    const activeSort = queryState.sort?.[0];
                    const sortDir =
                      activeSort?.column_id === column.id ? activeSort.direction : null;
                    return (
                      <th key={column.id} className="px-3 py-2 text-left">
                        <button
                          type="button"
                          className={cn(
                            "inline-flex items-center gap-1 hover:text-zinc-100",
                            sortDir ? "text-zinc-100" : "text-zinc-400",
                          )}
                          onClick={() => handleSortToggle(column.id)}
                          data-testid={`spreadsheet-review-sort-${column.id}`}
                        >
                          {column.label}
                          {sortDir ? (sortDir === "asc" ? "↑" : "↓") : null}
                        </button>
                      </th>
                    );
                  })}
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-900">
                {filteredRows.length === 0 ? (
                  <tr>
                    <td
                      colSpan={Math.max(visibleColumns.length + 1, 2)}
                      className="px-3 py-6 text-center text-zinc-500"
                      data-testid="spreadsheet-review-empty"
                    >
                      No rows match the current query.
                    </td>
                  </tr>
                ) : (
                  filteredRows.map((row) => (
                    <tr key={row.id} className="bg-zinc-950/30">
                      <td className="px-3 py-2 align-top">
                        <Checkbox
                          checked={selectedRowIDs.includes(row.id)}
                          onChange={(event) => handleToggleRow(row.id, event.currentTarget.checked)}
                          data-testid={`spreadsheet-review-select-${row.id}`}
                        />
                      </td>
                      {visibleColumns.map((column) => (
                        <td
                          key={`${row.id}:${column.id}`}
                          className="px-3 py-2 align-top text-zinc-200"
                        >
                          {displayCell(row[column.id])}
                        </td>
                      ))}
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>

          <div className="space-y-4">
            <section className="space-y-2 rounded-xl border border-zinc-800 p-4">
              <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
                Saved views
              </p>
              <div className="flex gap-2">
                <Input
                  value={saveViewName}
                  onChange={(event) => setSaveViewName(event.currentTarget.value)}
                  placeholder="View name"
                  data-testid="spreadsheet-review-view-name"
                />
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleSaveView}
                  data-testid="spreadsheet-review-save-view"
                >
                  Save
                </Button>
              </div>
              <div className="space-y-2">
                {savedViews.map((view) => (
                  <div
                    key={view.name}
                    className="flex items-center justify-between gap-2 rounded border border-zinc-800 px-3 py-2"
                  >
                    <span className="text-sm text-zinc-200">{view.name}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => handleRestoreView(view)}
                      data-testid={`spreadsheet-review-restore-${view.name}`}
                    >
                      Restore
                    </Button>
                  </div>
                ))}
                {savedViews.length === 0 ? (
                  <p className="text-sm text-zinc-500">No saved views yet.</p>
                ) : null}
              </div>
            </section>

            {rowActions.length > 0 ? (
              <section className="space-y-2 rounded-xl border border-zinc-800 p-4">
                <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">
                  Bulk action
                </p>
                <select
                  className="h-10 w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 text-sm"
                  value={actionID}
                  onChange={(event) => setActionID(event.currentTarget.value)}
                  data-testid="spreadsheet-review-action"
                >
                  <option value="">No action</option>
                  {rowActions.map((action) => (
                    <option key={action.id} value={action.id}>
                      {action.label}
                    </option>
                  ))}
                </select>
              </section>
            ) : null}

            <section className="space-y-2 rounded-xl border border-zinc-800 p-4">
              <p className="text-xs font-medium uppercase tracking-wide text-zinc-500">Notes</p>
              <Textarea
                rows={6}
                value={notes}
                onChange={(event) => setNotes(event.currentTarget.value)}
                data-testid="spreadsheet-review-notes"
              />
            </section>
          </div>
        </section>
      </CardContent>
      <CardFooter className="justify-end gap-3">
        <p className="mr-auto text-xs text-zinc-500">
          Draft changes recover in this browser until you submit or cancel.
        </p>
        <Button
          type="button"
          variant="outline"
          onClick={handleExport}
          data-testid="spreadsheet-review-export"
        >
          Export CSV
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={handleCancel}
          data-testid="spreadsheet-review-cancel"
        >
          Cancel
        </Button>
        <Button type="button" onClick={handleSubmit} data-testid="spreadsheet-review-submit">
          Submit review
        </Button>
      </CardFooter>
    </Card>
  );
}

function normalizeColumns(input: Array<Record<string, unknown>> | undefined): Column[] {
  if (!input || input.length === 0) {
    return [];
  }
  const out: Column[] = [];
  const seen = new Set<string>();
  for (const item of input) {
    const id = typeof item.id === "string" ? item.id.trim() : "";
    if (!id || seen.has(id)) {
      continue;
    }
    seen.add(id);
    out.push({
      id,
      label: typeof item.label === "string" && item.label.trim() ? item.label.trim() : id,
      sortable: item.sortable !== false,
    });
  }
  return out;
}

function normalizeRows(input: Array<Record<string, unknown>> | undefined): Row[] {
  if (!input || input.length === 0) {
    return [];
  }
  return input.map((row, index) => {
    const id =
      typeof row.id === "string" && row.id.trim()
        ? row.id.trim()
        : typeof row.row_id === "string" && row.row_id.trim()
          ? row.row_id.trim()
          : `row-${index + 1}`;
    return { ...row, id };
  });
}

function normalizeQueryState(queryState: QueryState | undefined, columns: Column[]): QueryState {
  const visibleColumns = normalizeStringArray(queryState?.visible_columns);
  return {
    sort:
      queryState?.sort?.filter(
        (item) =>
          item &&
          typeof item.column_id === "string" &&
          item.column_id.trim() &&
          (item.direction === "asc" || item.direction === "desc"),
      ) ?? [],
    filters:
      queryState?.filters?.filter(
        (item) =>
          item &&
          typeof item.column_id === "string" &&
          item.column_id.trim() &&
          typeof item.value === "string" &&
          item.value.trim() &&
          (item.op === "contains" || item.op === "eq"),
      ) ?? [],
    search: typeof queryState?.search === "string" ? queryState.search : "",
    visible_columns:
      visibleColumns.length > 0 ? visibleColumns : columns.map((column) => column.id),
    page: queryState?.page ?? { index: 1, size: 50 },
  };
}

function normalizeSavedViews(input: unknown): SavedView[] {
  if (!Array.isArray(input)) {
    return [];
  }
  return input
    .flatMap((item) => {
      if (!item || typeof item !== "object") {
        return [];
      }
      const view = item as Record<string, unknown>;
      const name = typeof view.name === "string" ? view.name.trim() : "";
      if (!name) {
        return [];
      }
      return [
        {
          name,
          query_state: (view.query_state as QueryState | undefined) ?? {},
        },
      ];
    })
    .filter(
      (view, index, all) => all.findIndex((candidate) => candidate.name === view.name) === index,
    );
}

function normalizeRowActions(input: RowAction[] | undefined): RowAction[] {
  if (!input || input.length === 0) {
    return [];
  }
  return input
    .flatMap((action) => {
      const id = typeof action.id === "string" ? action.id.trim() : "";
      if (!id) {
        return [];
      }
      const label =
        typeof action.label === "string" && action.label.trim() ? action.label.trim() : id;
      return [{ id, label, description: action.description?.trim() || undefined }];
    })
    .filter(
      (action, index, all) => all.findIndex((candidate) => candidate.id === action.id) === index,
    );
}

function normalizeExportRefs(input: unknown): ExportRef[] {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    if (!item || typeof item !== "object") {
      return [];
    }
    const ref = item as Record<string, unknown>;
    const name = typeof ref.name === "string" ? ref.name.trim() : "";
    if (!name) {
      return [];
    }
    return [
      {
        name,
        mime_type: typeof ref.mime_type === "string" ? ref.mime_type : undefined,
        kind: typeof ref.kind === "string" ? ref.kind : undefined,
        created_at: typeof ref.created_at === "string" ? ref.created_at : undefined,
        row_count: typeof ref.row_count === "number" ? ref.row_count : undefined,
        column_count: typeof ref.column_count === "number" ? ref.column_count : undefined,
        size_bytes: typeof ref.size_bytes === "number" ? ref.size_bytes : undefined,
      },
    ];
  });
}

function normalizeStringArray(values: unknown): string[] {
  if (!Array.isArray(values)) {
    return [];
  }
  return values.flatMap((value) =>
    typeof value === "string" && value.trim() ? [value.trim()] : [],
  );
}

function getFilterKey(filter: QueryFilter): string {
  return `${filter.column_id}:${filter.op}:${filter.value}`;
}

function applyQueryState(rows: Row[], visibleColumnIDs: string[], queryState: QueryState): Row[] {
  let next = [...rows];
  const search = queryState.search?.trim().toLowerCase();
  if (search) {
    next = next.filter((row) =>
      visibleColumnIDs.some((columnID) =>
        String(row[columnID] ?? "")
          .toLowerCase()
          .includes(search),
      ),
    );
  }
  for (const filter of queryState.filters ?? []) {
    const target = filter.value.toLowerCase();
    next = next.filter((row) => {
      const value = String(row[filter.column_id] ?? "").toLowerCase();
      if (filter.op === "eq") {
        return value === target;
      }
      return value.includes(target);
    });
  }
  const activeSort = queryState.sort?.[0];
  if (activeSort?.column_id) {
    next.sort((left, right) => {
      const a = String(left[activeSort.column_id] ?? "");
      const b = String(right[activeSort.column_id] ?? "");
      const cmp = a.localeCompare(b, undefined, {
        numeric: true,
        sensitivity: "base",
      });
      return activeSort.direction === "desc" ? -cmp : cmp;
    });
  }
  return next;
}

function compactRow(row: Row, visibleColumnIDs: string[]): Record<string, unknown> {
  const out: Record<string, unknown> = { id: row.id };
  for (const key of visibleColumnIDs) {
    const value = row[key];
    if (
      typeof value === "string" ||
      typeof value === "number" ||
      typeof value === "boolean" ||
      value === null
    ) {
      out[key] = value;
    }
  }
  return out;
}

function displayCell(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (value == null) {
    return "";
  }
  return JSON.stringify(value);
}

function buildCSV(columns: Column[], rows: Row[]): string {
  const header = columns.map((column) => escapeCSV(column.label)).join(",");
  const lines = rows.map((row) =>
    columns.map((column) => escapeCSV(String(row[column.id] ?? ""))).join(","),
  );
  return [header, ...lines].join("\n");
}

function escapeCSV(value: string): string {
  const escaped = value.replaceAll('"', '""');
  return /[",\n]/.test(escaped) ? `"${escaped}"` : escaped;
}

function sanitizeFilePart(value: string): string {
  return value.replace(/[^a-zA-Z0-9._-]+/g, "-");
}
