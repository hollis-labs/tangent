import type {
  SettingsChanges,
  SettingsGroup,
  SettingsValidation,
  SettingsValues,
} from "@hollis-labs/kit-settings";

export interface Scope {
  kind: string;
  id: string;
}
export interface Snapshot {
  plugin_id: string;
  scope: Scope;
  revision: string;
  schema_digest: string;
  values: SettingsValues & Record<string, { source?: string }>;
  pending_restart: boolean;
}
export interface Catalog {
  groups: SettingsGroup[];
  scopes: Scope[];
}
export interface SettingsAPI {
  read(id: string, scope: Scope, signal: AbortSignal): Promise<Snapshot>;
  action(
    id: string,
    scope: Scope,
    revision: string,
    action: string,
    changes: SettingsChanges,
    signal: AbortSignal,
  ): Promise<Snapshot | SettingsValidation>;
}

// Refusals use host-owned text. Neither provider errors nor submitted secrets
// enter browser diagnostics, even if a proxy returns an unexpected error body.
async function request<T>(path: string, init: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { Accept: "application/json", ...init.headers },
  });
  if (!response.ok && response.status !== 422) {
    throw new Error(
      response.status === 409
        ? "Settings operation refused. Refresh before saving again."
        : "Settings are unavailable for this participant.",
    );
  }
  return response.json() as Promise<T>;
}
const base = (id: string) => `/api/plugin-management/${encodeURIComponent(id)}/config`;
export const settingsAPI: SettingsAPI = {
  read: (id, scope, signal) =>
    request(`${base(id)}?${new URLSearchParams({ scope_kind: scope.kind, scope_id: scope.id })}`, {
      signal,
    }),
  action: (id, scope, revision, action, changes, signal) =>
    request(`${base(id)}/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope, revision, changes }),
      signal,
    }),
};
export const fetchSettingsCatalog = (signal: AbortSignal): Promise<Catalog> =>
  request("/api/plugin-management/config", { signal });
