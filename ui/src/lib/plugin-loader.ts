/**
 * Plugin loader integration for CW-20260911-0035 minimal proof.
 *
 * Fetches the registry from /api/plugins/registry and resolves plugins via
 * @hollis-labs/plugin-registry. This is the second real consumer of the
 * extracted package (Nanite is the reference implementation).
 *
 * Minimal proof scope: demonstrates the wire contract works end-to-end.
 * Full production wiring (React integration, contribution rendering, CSP
 * hardening) is follow-on adoption work.
 */

import { createPluginRegistry } from "@hollis-labs/plugin-registry";

// The registry instance, created once at module load.
const pluginRegistry = createPluginRegistry({
  onDiagnostic: (event) => {
    // Minimal proof logging — production would route to proper telemetry.
    console.info("[plugin-loader]", event.type, event);
  },
});

/**
 * Fetches and syncs the plugin registry from the host.
 *
 * Returns the registry sync result. Logs errors rather than throwing —
 * a plugin load failure should not break the app.
 */
export async function syncPlugins(): Promise<void> {
  try {
    const response = await fetch("/api/plugins/registry");
    if (!response.ok) {
      console.error(`[plugin-loader] registry fetch failed: ${response.status}`);
      return;
    }

    const data = await response.json();

    // Sync the registry with the fetched response.
    const result = await pluginRegistry.sync(data);

    console.info("[plugin-loader] plugins synced", {
      protocol: data.protocol,
      accepted: result.accepted,
      loaded: result.loaded.length,
      failed: result.failed.length,
      declared: result.declared,
      resolved: result.resolved,
      refused: result.refused,
    });
  } catch (err) {
    console.error("[plugin-loader] sync failed:", err);
  }
}

/**
 * Exported for future use when contribution resolution is wired.
 * Not used in the minimal proof.
 */
export { pluginRegistry };
