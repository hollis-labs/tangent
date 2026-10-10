import "@hollis-labs/kit-settings/source.css";
import { type SettingsGroup, SettingsGroupForm } from "@hollis-labs/kit-settings";
import { useEffect, useRef, useState } from "react";
import { type Catalog, fetchSettingsCatalog, type Scope, settingsAPI } from "./api";
import { SettingsController, type ViewState } from "./controller";

function PluginGroup({ group, scope }: { group: SettingsGroup; scope: Scope }) {
  const [state, setState] = useState<ViewState>({ values: {}, draft: {}, busy: true });
  const controller = useRef<SettingsController | null>(null);
  useEffect(() => {
    const owned = new SettingsController(settingsAPI, group.id, scope, setState);
    controller.current = owned;
    void owned.refresh();
    return () => {
      owned.dispose();
      controller.current = null;
    };
  }, [group.id, scope]);
  return (
    <SettingsGroupForm
      group={group}
      {...state}
      unavailable={state.snapshot ? undefined : "Settings snapshot unavailable."}
      onDraftChange={(draft) => controller.current?.draft(draft)}
      onSave={(changes) => {
        void controller.current?.action("save", changes);
      }}
      onValidate={(changes) => {
        void controller.current?.action("validate", changes);
      }}
      onReset={(keys) => {
        void controller.current?.action("reset", { set: {}, unset: keys });
      }}
      fieldExtra={(key) => (
        <span className="text-xs text-fg-muted">
          {state.snapshot?.values[key]?.source
            ? `Source: ${state.snapshot.values[key].source}`
            : "Not configured"}
        </span>
      )}
      footer={
        <div className="flex gap-3 items-center">
          <button
            type="button"
            disabled={state.busy}
            onClick={() => {
              void controller.current?.refresh();
            }}
          >
            Refresh
          </button>
          {state.snapshot?.pending_restart && (
            <>
              <span>Saved settings await restart.</span>
              <button
                type="button"
                disabled={state.busy || Object.keys(state.draft).length > 0}
                onClick={() => {
                  void controller.current?.action("apply", { set: {}, unset: [] });
                }}
              >
                Apply and restart
              </button>
            </>
          )}
        </div>
      }
    />
  );
}
export function PluginSettings() {
  const [catalog, setCatalog] = useState<Catalog>();
  const [selected, setSelected] = useState(0);
  const [error, setError] = useState(false);
  useEffect(() => {
    const request = new AbortController();
    void fetchSettingsCatalog(request.signal)
      .then((result) => {
        if (!request.signal.aborted) setCatalog(result);
      })
      .catch(() => {
        if (!request.signal.aborted) setError(true);
      });
    return () => request.abort();
  }, []);
  if (error) return <p role="alert">Plugin settings are unavailable for this participant.</p>;
  if (!catalog) return <p>Loading plugin settings…</p>;
  if (!catalog.groups.length || !catalog.scopes.length)
    return <p>No installed plugin declares editable settings.</p>;
  const scope = catalog.scopes[selected];
  return (
    <section className="px-4 py-5 sm:px-6 lg:px-8" aria-label="Plugin settings">
      <label>
        Settings scope{" "}
        <select value={selected} onChange={(event) => setSelected(Number(event.target.value))}>
          {catalog.scopes.map((s, i) => (
            <option key={`${s.kind}:${s.id}`} value={i}>
              {s.kind}: {s.id}
            </option>
          ))}
        </select>
      </label>
      <p>
        Secrets are stored in the host keychain. Only their presence is shown. Save persists
        settings; apply restarts the plugin.
      </p>
      {catalog.groups.map((group) => (
        <PluginGroup key={`${group.id}:${scope.kind}:${scope.id}`} group={group} scope={scope} />
      ))}
    </section>
  );
}
