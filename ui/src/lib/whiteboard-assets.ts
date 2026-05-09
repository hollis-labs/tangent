import type { TLEditorSnapshot } from "tldraw";

export interface WhiteboardAssetRef {
  asset_id?: string;
  artifact_id?: string;
  name?: string;
  mime_type?: string;
  source?: string;
  uri?: string;
  kind?: "reference_image";
  width?: number;
  height?: number;
}

export interface WhiteboardExportRef {
  artifact_id?: string;
  name?: string;
  mime_type?: string;
  kind?: "png";
  uri?: string;
  created_at?: string;
  size_bytes?: number;
  width?: number;
  height?: number;
}

type AssetStatus = {
  staleReferenceImages: WhiteboardAssetRef[];
  unsupportedLocalAssetIds: string[];
};

export function prepareWhiteboardSnapshotForEditor(
  scene: TLEditorSnapshot | Record<string, unknown> | undefined,
  assetRefs: WhiteboardAssetRef[],
): {
  snapshot: TLEditorSnapshot | undefined;
  staleReferenceImages: WhiteboardAssetRef[];
} {
  if (!scene) {
    return { snapshot: undefined, staleReferenceImages: [] };
  }
  const snapshot = cloneSnapshot(scene) as TLEditorSnapshot;
  const staleByKey = new Map<string, WhiteboardAssetRef>();
  visitImageAssetRecords(snapshot, (record, assetID) => {
    const ref = findMatchingAssetRef(assetRefs, assetID, readAssetRecordSource(record));
    if (!ref) {
      return;
    }
    const renderableSource = readRenderableSource(ref);
    if (renderableSource) {
      setAssetRecordSource(record, renderableSource);
      return;
    }
    const key = assetRefKey(ref, assetID);
    if (key) {
      staleByKey.set(key, ref);
    }
  });
  return {
    snapshot,
    staleReferenceImages: Array.from(staleByKey.values()),
  };
}

export function buildWhiteboardSubmitAssets(
  scene: TLEditorSnapshot,
  assetRefs: WhiteboardAssetRef[],
): {
  snapshot: TLEditorSnapshot;
  assets: WhiteboardAssetRef[];
  unsupportedLocalAssetIds: string[];
} {
  const snapshot = cloneSnapshot(scene) as TLEditorSnapshot;
  const collected: WhiteboardAssetRef[] = [];
  const status = inspectSnapshotAssets(snapshot, assetRefs, collected);
  return {
    snapshot,
    assets: mergeWhiteboardAssetRefs(assetRefs, collected),
    unsupportedLocalAssetIds: status.unsupportedLocalAssetIds,
  };
}

function inspectSnapshotAssets(
  snapshot: TLEditorSnapshot,
  assetRefs: WhiteboardAssetRef[],
  collected: WhiteboardAssetRef[],
): AssetStatus {
  const staleByKey = new Map<string, WhiteboardAssetRef>();
  const unsupported = new Set<string>();
  visitImageAssetRecords(snapshot, (record, assetID) => {
    const rawSource = readAssetRecordSource(record);
    const ref = findMatchingAssetRef(assetRefs, assetID, rawSource);
    const durableURI = readDurableURI(ref) ?? (isArtifactURI(rawSource) ? rawSource : undefined);
    if (durableURI) {
      setAssetRecordSource(record, durableURI);
    } else if (isBrowserOnlySource(rawSource)) {
      unsupported.add(assetID);
    }

    const renderableSource = readRenderableSource(ref);
    if (!durableURI && !renderableSource) {
      const key = assetRefKey(ref, assetID);
      if (key && ref) {
        staleByKey.set(key, ref);
      }
    }

    if (!durableURI && !ref) {
      return;
    }

    const props = readAssetRecordProps(record);
    const nextAsset: WhiteboardAssetRef = {
      asset_id: assetID,
      artifact_id: ref?.artifact_id ?? artifactIDFromURI(durableURI),
      name: ref?.name ?? readString(props, "name"),
      mime_type: ref?.mime_type ?? readString(props, "mimeType"),
      source: renderableSource,
      uri: durableURI,
      kind: ref?.kind,
      width: ref?.width ?? readNumber(props, "w"),
      height: ref?.height ?? readNumber(props, "h"),
    };
    collected.push(nextAsset);
  });
  return {
    staleReferenceImages: Array.from(staleByKey.values()),
    unsupportedLocalAssetIds: Array.from(unsupported.values()),
  };
}

export function mergeWhiteboardAssetRefs(...groups: WhiteboardAssetRef[][]): WhiteboardAssetRef[] {
  const seen = new Map<string, number>();
  const merged: WhiteboardAssetRef[] = [];
  for (const group of groups) {
    for (const asset of group) {
      const key = assetRefKey(asset, asset.asset_id);
      if (!key) {
        continue;
      }
      const existingIndex = seen.get(key);
      if (existingIndex !== undefined) {
        merged[existingIndex] = mergeAssetRefFields(merged[existingIndex], asset);
        continue;
      }
      seen.set(key, merged.length);
      merged.push({ ...asset });
    }
  }
  return merged;
}

function mergeAssetRefFields(
  current: WhiteboardAssetRef,
  incoming: WhiteboardAssetRef,
): WhiteboardAssetRef {
  return {
    asset_id: current.asset_id ?? incoming.asset_id,
    artifact_id: current.artifact_id ?? incoming.artifact_id,
    name: current.name ?? incoming.name,
    mime_type: current.mime_type ?? incoming.mime_type,
    source: current.source ?? incoming.source,
    uri: current.uri ?? incoming.uri,
    kind: current.kind ?? incoming.kind,
    width: current.width ?? incoming.width,
    height: current.height ?? incoming.height,
  };
}

function findMatchingAssetRef(
  assetRefs: WhiteboardAssetRef[],
  assetID: string,
  source: string | undefined,
): WhiteboardAssetRef | null {
  for (const asset of assetRefs) {
    if (asset.asset_id && asset.asset_id === assetID) {
      return asset;
    }
  }
  const durableSource = isArtifactURI(source) ? source : undefined;
  if (durableSource) {
    for (const asset of assetRefs) {
      if (readDurableURI(asset) === durableSource) {
        return asset;
      }
    }
  }
  if (source) {
    for (const asset of assetRefs) {
      if (asset.source === source) {
        return asset;
      }
    }
  }
  return null;
}

function visitImageAssetRecords(
  snapshot: TLEditorSnapshot,
  visitor: (record: Record<string, unknown>, assetID: string) => void,
): void {
  const store = readObject(snapshot, "store");
  for (const [recordID, raw] of Object.entries(store)) {
    if (!raw || typeof raw !== "object") {
      continue;
    }
    const record = raw as Record<string, unknown>;
    if (record.typeName !== "asset" || record.type !== "image") {
      continue;
    }
    visitor(record, typeof record.id === "string" ? record.id : recordID);
  }
}

function readAssetRecordProps(record: Record<string, unknown>): Record<string, unknown> {
  const props = record.props;
  return props && typeof props === "object" ? (props as Record<string, unknown>) : {};
}

function readAssetRecordSource(record: Record<string, unknown>): string | undefined {
  return readString(readAssetRecordProps(record), "src");
}

function setAssetRecordSource(record: Record<string, unknown>, source: string): void {
  const props = readAssetRecordProps(record);
  record.props = { ...props, src: source };
}

function readRenderableSource(asset: WhiteboardAssetRef | null): string | undefined {
  if (!asset?.source || isArtifactURI(asset.source) || isBrowserOnlySource(asset.source)) {
    return undefined;
  }
  return asset.source;
}

function readDurableURI(asset: WhiteboardAssetRef | null): string | undefined {
  if (!asset) {
    return undefined;
  }
  if (asset.uri) {
    return asset.uri;
  }
  if (asset.source && isArtifactURI(asset.source)) {
    return asset.source;
  }
  if (asset.artifact_id) {
    return `artifact://${asset.artifact_id}`;
  }
  return undefined;
}

function artifactIDFromURI(uri: string | undefined): string | undefined {
  if (!uri?.startsWith("artifact://")) {
    return undefined;
  }
  const artifactID = uri.slice("artifact://".length);
  return artifactID.length > 0 ? artifactID : undefined;
}

function assetRefKey(
  asset: WhiteboardAssetRef | null | undefined,
  fallbackAssetID?: string,
): string {
  if (!asset && !fallbackAssetID) {
    return "";
  }
  if (asset?.asset_id) {
    return `asset:${asset.asset_id}`;
  }
  if (fallbackAssetID) {
    return `asset:${fallbackAssetID}`;
  }
  const durableURI = readDurableURI(asset ?? null);
  if (durableURI) {
    return `uri:${durableURI}`;
  }
  if (asset?.artifact_id) {
    return `artifact:${asset.artifact_id}`;
  }
  if (asset?.source) {
    return `source:${asset.source}`;
  }
  return "";
}

function cloneSnapshot<T>(value: T): T {
  return typeof structuredClone === "function"
    ? structuredClone(value)
    : (JSON.parse(JSON.stringify(value)) as T);
}

function readObject(input: unknown, key: string): Record<string, unknown> {
  if (!input || typeof input !== "object") {
    return {};
  }
  const value = (input as Record<string, unknown>)[key];
  return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function readString(input: Record<string, unknown>, key: string): string | undefined {
  const value = input[key];
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function readNumber(input: Record<string, unknown>, key: string): number | undefined {
  const value = input[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function isArtifactURI(value: string | undefined): boolean {
  return typeof value === "string" && value.startsWith("artifact://");
}

function isBrowserOnlySource(value: string | undefined): boolean {
  return typeof value === "string" && (value.startsWith("blob:") || value.startsWith("data:"));
}
