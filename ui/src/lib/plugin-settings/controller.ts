import type { SettingsChanges, SettingsDraft, SettingsState } from "@hollis-labs/kit-settings";
import type { Scope, SettingsAPI, Snapshot } from "./api";

export interface ViewState extends SettingsState {
  snapshot?: Snapshot;
}
// One controller belongs to one keyed plugin/scope view. No draft, including
// secret replacements, is persisted or lent to another plugin or scope.
export class SettingsController {
  private active = true;
  private refreshing = false;
  private request = new AbortController();
  private state: ViewState = { values: {}, draft: {}, busy: true };
  private readonly api: SettingsAPI;
  private readonly id: string;
  private readonly scope: Scope;
  private readonly changed: (state: ViewState) => void;
  constructor(api: SettingsAPI, id: string, scope: Scope, changed: (state: ViewState) => void) {
    this.api = api;
    this.id = id;
    this.scope = scope;
    this.changed = changed;
  }
  private publish(state: ViewState) {
    if (this.active) {
      this.state = state;
      this.changed(state);
    }
  }
  draft(draft: SettingsDraft) {
    if (!this.state.busy)
      this.publish({
        ...this.state,
        draft,
        validation: undefined,
        error: undefined,
        notice: undefined,
      });
  }
  async refresh() {
    if (!this.active || this.refreshing || (this.state.busy && this.state.snapshot)) return;
    this.refreshing = true;
    this.publish({ ...this.state, busy: true, error: undefined });
    try {
      const snapshot = await this.api.read(this.id, this.scope, this.request.signal);
      this.publish({ ...this.state, snapshot, values: snapshot.values, busy: false });
    } catch {
      this.publish({ ...this.state, busy: false, error: "Settings could not be refreshed." });
    } finally {
      this.refreshing = false;
    }
  }
  async action(action: "save" | "validate" | "reset" | "apply", changes: SettingsChanges) {
    const snapshot = this.state.snapshot;
    if (!this.active || this.state.busy || !snapshot) return;
    this.publish({ ...this.state, busy: true, error: undefined, validation: undefined });
    try {
      const result = await this.api.action(
        this.id,
        this.scope,
        snapshot.revision,
        action,
        changes,
        this.request.signal,
      );
      if ("valid" in result) {
        this.publish({ ...this.state, busy: false, validation: result });
        return;
      }
      this.publish({
        ...this.state,
        snapshot: result,
        values: result.values,
        draft: {},
        busy: false,
        notice:
          action === "apply"
            ? result.pending_restart
              ? "Plugin restarted; a newer saved revision still awaits apply."
              : "Plugin restarted with the saved configuration."
            : "Saved. Restart the plugin to apply this configuration.",
      });
    } catch {
      this.publish({
        ...this.state,
        busy: false,
        error: "Settings operation refused. Refresh before saving again.",
      });
    }
  }
  dispose() {
    this.active = false;
    this.request.abort();
    this.state = { values: {}, draft: {} };
  }
}
