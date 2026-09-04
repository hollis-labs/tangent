import type { ArtifactPreview, EvidenceAPIError, TangentReferenceView } from "./types";

export class EvidenceRequestError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, body: Partial<EvidenceAPIError>) {
    super(body.message || "The durable evidence could not be loaded.");
    this.name = "EvidenceRequestError";
    this.status = status;
    this.code = body.code || "evidence_unavailable";
  }
}

export function fetchTangentReference(
  itemID: string,
  evidenceIndex: number,
  signal?: AbortSignal,
): Promise<TangentReferenceView> {
  return evidenceRequest<TangentReferenceView>(itemID, evidenceIndex, "reference", signal);
}

export function fetchArtifactPreview(
  itemID: string,
  evidenceIndex: number,
  signal?: AbortSignal,
): Promise<ArtifactPreview> {
  return evidenceRequest<ArtifactPreview>(itemID, evidenceIndex, "preview", signal);
}

async function evidenceRequest<T>(
  itemID: string,
  evidenceIndex: number,
  operation: "reference" | "preview",
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(
    `/api/hitl/items/${encodeURIComponent(itemID)}/evidence/${evidenceIndex}/${operation}`,
    { headers: { Accept: "application/json" }, signal },
  );
  const body = (await response.json().catch(() => ({}))) as T | Partial<EvidenceAPIError>;
  if (!response.ok)
    throw new EvidenceRequestError(response.status, body as Partial<EvidenceAPIError>);
  return body as T;
}
