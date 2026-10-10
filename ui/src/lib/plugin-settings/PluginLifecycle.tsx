import { useEffect, useRef, useState } from "react";

interface PluginState {
  id: string;
  enabled: boolean;
  loaded: boolean;
  state: string;
}

async function readInventory(signal: AbortSignal): Promise<PluginState[]> {
  const response = await fetch("/api/plugin-management", {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error("Plugin state is unavailable for this participant.");
  const inventory = (await response.json()) as { plugins: PluginState[] };
  return inventory.plugins;
}

export function PluginLifecycle() {
  const [plugins, setPlugins] = useState<PluginState[]>([]);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string>();
  const request = useRef<AbortController | null>(null);
  useEffect(() => {
    const owned = new AbortController();
    request.current = owned;
    void readInventory(owned.signal)
      .then((plugins) => {
        if (!owned.signal.aborted) setPlugins(plugins);
      })
      .catch(() => {
        if (!owned.signal.aborted) setError("Plugin state is unavailable for this participant.");
      })
      .finally(() => {
        if (!owned.signal.aborted) setBusy(false);
      });
    return () => request.current?.abort();
  }, []);

  const act = async (id?: string, action?: string) => {
    request.current?.abort();
    const owned = new AbortController();
    request.current = owned;
    setBusy(true);
    setError(undefined);
    try {
      let refused = false;
      if (id && action) {
        const response = await fetch(`/api/plugin-management/${encodeURIComponent(id)}/${action}`, {
          method: "POST",
          signal: owned.signal,
          credentials: "same-origin",
        });
        refused = !response.ok;
      }
      // A failed start can still save enabled intent. Refresh actual state
      // even when the lifecycle action refuses; never infer readiness from POST.
      const current = await readInventory(owned.signal);
      if (!owned.signal.aborted) setPlugins(current);
      if (refused && !owned.signal.aborted)
        setError("Plugin operation refused. Current intent and runtime state are shown.");
    } catch {
      if (!owned.signal.aborted)
        setError("Plugin operation unavailable. Refresh state before trying again.");
    } finally {
      if (!owned.signal.aborted) setBusy(false);
    }
  };

  return (
    <section className="px-4 py-5 sm:px-6 lg:px-8" aria-label="Plugin lifecycle">
      <h2 className="text-lg font-semibold">Installed plugins</h2>
      <p>
        New plugins stay disabled until you explicitly enable them. Saving settings does not enable
        a plugin.
      </p>
      {error && <p role="alert">{error}</p>}
      <button type="button" disabled={busy} onClick={() => void act()}>
        Refresh plugin state
      </button>
      {busy && <p role="status">Loading plugin state…</p>}
      {!busy && !plugins.length && <p>No installed plugins discovered.</p>}
      <ul>
        {plugins.map((plugin) => (
          <li key={plugin.id} className="mt-4 flex flex-wrap items-center gap-3">
            <span>{plugin.id}</span>
            <span>Intent: {plugin.enabled ? "enabled" : "disabled"}</span>
            <span>
              Runtime: {plugin.state} ({plugin.loaded ? "loaded" : "not loaded"})
            </span>
            <button
              type="button"
              disabled={busy}
              onClick={() => void act(plugin.id, plugin.enabled ? "disable" : "enable")}
            >
              {plugin.enabled ? "Disable" : "Enable"} {plugin.id}
            </button>
            <button
              type="button"
              disabled={busy || !plugin.enabled}
              onClick={() => void act(plugin.id, "reload")}
            >
              Reload {plugin.id}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
