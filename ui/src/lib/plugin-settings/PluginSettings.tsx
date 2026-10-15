import { Button, Callout } from "@hollis-labs/design-components";
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
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={state.busy}
            onClick={() => {
              void controller.current?.refresh();
            }}
          >
            Refresh
          </Button>
          {state.snapshot?.pending_restart && (
            <>
              <span className="text-control text-fg-muted">Saved settings await restart.</span>
              <Button
                type="button"
                size="sm"
                disabled={state.busy || Object.keys(state.draft).length > 0}
                onClick={() => {
                  void controller.current?.action("apply", { set: {}, unset: [] });
                }}
              >
                Apply and restart
              </Button>
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
  const note = "px-4 py-5 text-control text-fg-muted sm:px-6 lg:px-8";
  if (error)
    return (
      <div className="px-4 py-5 sm:px-6 lg:px-8">
        <Callout tone="danger">Plugin settings are unavailable for this participant.</Callout>
      </div>
    );
  if (!catalog) return <p className={note}>Loading plugin settings…</p>;
  if (!catalog.groups.length || !catalog.scopes.length)
    return <p className={note}>No installed plugin declares editable settings.</p>;
  const scope = catalog.scopes[selected];
  return (
    <section className="flex flex-col gap-4 px-4 py-5 sm:px-6 lg:px-8" aria-label="Plugin settings">
      <label className="flex items-center gap-2 text-label font-medium text-fg-secondary">
        Settings scope
        <select
          className="h-8 rounded-control border border-input bg-bg px-2 text-control text-fg outline-none focus-visible:ring-2 focus-visible:ring-ring"
          value={selected}
          onChange={(event) => setSelected(Number(event.target.value))}
        >
          {catalog.scopes.map((s, i) => (
            <option key={`${s.kind}:${s.id}`} value={i}>
              {s.kind}: {s.id}
            </option>
          ))}
        </select>
      </label>
      <p className="max-w-2xl text-control text-fg-muted">
        Secrets are stored in the host keychain. Only their presence is shown. Save persists
        settings; apply restarts the plugin.
      </p>
      {catalog.groups.map((group) => (
        <PluginGroup key={`${group.id}:${scope.kind}:${scope.id}`} group={group} scope={scope} />
      ))}
    </section>
  );
}
