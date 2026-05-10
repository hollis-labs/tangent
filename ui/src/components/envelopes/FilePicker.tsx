import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

type BrowseRoot = {
  root_id: string;
  label?: string;
  path: string;
  kind?: string;
};

type FilePickerRef = {
  artifact_id?: string;
  name?: string;
  uri?: string;
  mime_type?: string;
  kind?: string;
  size_bytes?: number;
  root_id: string;
  relative_path: string;
};

type QueryState = {
  current_root_id?: string;
  [key: string]: unknown;
};

export interface FilePickerEnvelope {
  v: number;
  id: string;
  type: "tangent.file-picker";
  title?: string;
  context?: string;
  data?: {
    picker_id: string;
    browse_roots?: BrowseRoot[];
    selected_refs?: FilePickerRef[];
    query_state?: QueryState;
    files?: FilePickerRef[];
    updated_at?: string;
  };
}

export interface FilePickerResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    picker_id: string;
    selected_refs: FilePickerRef[];
    query_state: QueryState;
  };
  completedAt?: string;
}

type FilePickerProps = {
  envelope: FilePickerEnvelope;
  onSubmit: (response: FilePickerResponse) => void;
  onCancel: () => void;
};

export function FilePicker({ envelope, onSubmit, onCancel }: FilePickerProps) {
  const pickerID = envelope.data?.picker_id ?? "";
  const browseRoots = useMemo(
    () => normalizeBrowseRoots(envelope.data?.browse_roots),
    [envelope.data],
  );
  const initialSelected = useMemo(
    () => normalizeRefs(envelope.data?.selected_refs),
    [envelope.data?.selected_refs],
  );
  const availableFiles = useMemo(
    () => buildAvailableFiles(envelope.data?.files, initialSelected),
    [envelope.data?.files, initialSelected],
  );
  const [activeRootID, setActiveRootID] = useState(
    () =>
      normalizeQueryState(envelope.data?.query_state).current_root_id ??
      browseRoots[0]?.root_id ??
      "",
  );
  const [selectedKeys, setSelectedKeys] = useState(() => new Set(initialSelected.map(refKey)));

  const visibleFiles = useMemo(
    () => availableFiles.filter((item) => !activeRootID || item.root_id === activeRootID),
    [activeRootID, availableFiles],
  );

  const selectedRefs = useMemo(
    () => availableFiles.filter((item) => selectedKeys.has(refKey(item))),
    [availableFiles, selectedKeys],
  );

  return (
    <Card data-testid="file-picker-root">
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? "File picker"}</CardTitle>
        <p className="text-sm text-zinc-400">
          Select durable artifact-backed files from the allowed roots, then submit one canonical
          selection.
        </p>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
        <div className="space-y-3">
          <div className="space-y-2">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">Roots</p>
            <div className="space-y-2" data-testid="file-picker-roots">
              {browseRoots.map((root) => (
                <button
                  key={root.root_id}
                  type="button"
                  data-testid={`file-picker-root-${root.root_id}`}
                  onClick={() => setActiveRootID(root.root_id)}
                  className={cn(
                    "w-full rounded-xl border px-3 py-3 text-left transition",
                    activeRootID === root.root_id
                      ? "border-emerald-500 bg-emerald-500/10 text-zinc-50"
                      : "border-zinc-800 bg-zinc-950/60 text-zinc-300 hover:border-zinc-700",
                  )}
                >
                  <div className="font-medium">{root.label ?? root.root_id}</div>
                  <div className="text-xs text-zinc-500">{root.path}</div>
                </button>
              ))}
            </div>
          </div>
          <div className="rounded-xl border border-zinc-800 bg-zinc-950/50 p-3">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
              Selected
            </p>
            <p
              className="mt-2 text-2xl font-semibold text-zinc-50"
              data-testid="file-picker-selected-count"
            >
              {selectedRefs.length}
            </p>
            <p className="text-xs text-zinc-500">artifact-backed files queued for submit</p>
          </div>
        </div>

        <div className="space-y-3">
          <div className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="text-sm font-medium text-zinc-100">
                  {readRootLabel(browseRoots, activeRootID) ?? "Available files"}
                </p>
                <p className="text-xs text-zinc-500">
                  {visibleFiles.length > 0
                    ? "Selections are submitted as durable artifact refs."
                    : "No file candidates are loaded for this root yet."}
                </p>
              </div>
            </div>
            <div className="mt-3 space-y-2" data-testid="file-picker-files">
              {visibleFiles.map((item) => {
                const key = refKey(item);
                const checked = selectedKeys.has(key);
                return (
                  <label
                    key={key}
                    data-testid={`file-picker-file-${key}`}
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-xl border px-3 py-3 transition",
                      checked
                        ? "border-emerald-500 bg-emerald-500/10"
                        : "border-zinc-800 bg-zinc-950/40 hover:border-zinc-700",
                    )}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => {
                        setSelectedKeys((current) => {
                          const next = new Set(current);
                          if (next.has(key)) {
                            next.delete(key);
                          } else {
                            next.add(key);
                          }
                          return next;
                        });
                      }}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-zinc-100">
                        {item.name ?? basename(item.relative_path)}
                      </p>
                      <p className="truncate text-xs text-zinc-500">{item.relative_path}</p>
                      {item.uri ? (
                        <p className="truncate text-xs text-zinc-600">{item.uri}</p>
                      ) : null}
                    </div>
                  </label>
                );
              })}
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              data-testid="file-picker-submit"
              onClick={() =>
                onSubmit({
                  v: 1,
                  envelopeId: envelope.id,
                  kind: "data",
                  status: "submitted",
                  payload: {
                    picker_id: pickerID,
                    selected_refs: selectedRefs,
                    query_state: {
                      ...normalizeQueryState(envelope.data?.query_state),
                      current_root_id: activeRootID || undefined,
                    },
                  },
                })
              }
            >
              Submit selection
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function normalizeBrowseRoots(input: BrowseRoot[] | undefined): BrowseRoot[] {
  return (input ?? [])
    .map((item) => ({
      root_id: readString(item.root_id),
      label: readString(item.label) || undefined,
      path: readString(item.path),
      kind: readString(item.kind) || undefined,
    }))
    .filter((item) => item.root_id && item.path);
}

function normalizeRefs(input: FilePickerRef[] | undefined): FilePickerRef[] {
  return (input ?? [])
    .map((item) => ({
      artifact_id: readString(item.artifact_id) || undefined,
      name: readString(item.name) || undefined,
      uri: readString(item.uri) || undefined,
      mime_type: readString(item.mime_type) || undefined,
      kind: readString(item.kind) || undefined,
      size_bytes: typeof item.size_bytes === "number" ? item.size_bytes : undefined,
      root_id: readString(item.root_id),
      relative_path: readString(item.relative_path),
    }))
    .filter((item) => item.root_id && item.relative_path);
}

function normalizeQueryState(input: QueryState | undefined): QueryState {
  return input && typeof input === "object" ? { ...input } : {};
}

function buildAvailableFiles(
  input: FilePickerRef[] | undefined,
  selected: FilePickerRef[],
): FilePickerRef[] {
  const seen = new Set<string>();
  const out: FilePickerRef[] = [];
  for (const item of [...normalizeRefs(input), ...selected]) {
    const key = refKey(item);
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(item);
  }
  return out;
}

function refKey(item: Pick<FilePickerRef, "root_id" | "relative_path">): string {
  return `${item.root_id}:${item.relative_path}`;
}

function readRootLabel(roots: BrowseRoot[], rootID: string): string | null {
  return roots.find((item) => item.root_id === rootID)?.label ?? null;
}

function basename(value: string): string {
  const parts = value.split("/");
  return parts[parts.length - 1] ?? value;
}

function readString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}
