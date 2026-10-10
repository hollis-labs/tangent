// Synthetic fixture only. Never imported by the application entry point.
import type { UiCommand, UiEvent, UiScope, UiTransport, ViewDescriptor } from "@/lib/ui-channel";
export class SyntheticUiTransport implements UiTransport {
  frames: Record<string, unknown>[] = [];
  listeners = new Set<(event: UiEvent) => void>();
  online = true;
  revision = 0;
  sequence = 0;
  onSend?: (frame: Record<string, unknown>) => void;
  connected = () => this.online;
  subscribe = (listener: (event: UiEvent) => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  send = (frame: object) => {
    if (!this.online) return false;
    const detached = JSON.parse(JSON.stringify(frame)) as Record<string, unknown>;
    this.frames.push(detached);
    this.onSend?.(detached);
    return true;
  };
  emit(event: UiEvent) {
    for (const listener of [...this.listeners]) listener(event);
  }
  publish(revision = this.revision + 1) {
    this.revision = revision;
    this.emit({ type: "view.published", view_revision: revision });
  }
  command(
    name: string,
    args: Record<string, unknown>,
    scope: UiScope = "ephemeral",
    revision = this.revision,
  ): UiCommand {
    const event: UiCommand = {
      type: "ui.command",
      command_id: `synthetic-${++this.sequence}`,
      view_revision: revision,
      name,
      scope,
      arguments: args,
    };
    this.emit(event);
    return event;
  }
  descriptor(): ViewDescriptor {
    return this.frames.filter((frame) => frame.type === "view.publish").at(-1)
      ?.descriptor as ViewDescriptor;
  }
  ack() {
    return this.frames.filter((frame) => frame.type === "ui.ack").at(-1)?.ack as {
      status: string;
      view_revision: number;
    };
  }
}
