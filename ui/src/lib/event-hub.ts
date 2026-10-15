// One EventSource per URL, shared by every subscriber on the page.
//
// Browsers cap HTTP/1.1 connections per origin at six, and each Inbox, Chat or Docs
// route used to open its own copies of the same /events streams. The hub keeps a
// single connection per URL, reference-counted, and closes it when the last
// subscriber leaves. `connected` lets a subscriber run a slow fallback poll only
// while the stream is down.

type Listener = () => void;

interface Entry {
  source: EventSource;
  revisionListeners: Set<Listener>;
  statusListeners: Set<(connected: boolean) => void>;
  connected: boolean;
}

const entries = new Map<string, Entry>();

export interface EventSubscription {
  close(): void;
}

export function subscribeRevision(
  url: string,
  onRevision: Listener,
  onStatus?: (connected: boolean) => void,
): EventSubscription {
  let entry = entries.get(url);
  if (!entry) {
    const source = new EventSource(url);
    const created: Entry = {
      source,
      revisionListeners: new Set(),
      statusListeners: new Set(),
      connected: false,
    };
    const setConnected = (value: boolean) => {
      if (created.connected === value) return;
      created.connected = value;
      for (const listener of created.statusListeners) listener(value);
    };
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);
    source.addEventListener("revision", () => {
      setConnected(true);
      for (const listener of created.revisionListeners) listener();
    });
    entries.set(url, created);
    entry = created;
  }
  const held = entry;
  held.revisionListeners.add(onRevision);
  if (onStatus) {
    held.statusListeners.add(onStatus);
    // Report the current state either way: a subscriber that never hears "down" would
    // keep its slow fallback poll if the stream fails before it ever opens.
    onStatus(held.connected);
  }
  let closed = false;
  return {
    close() {
      if (closed) return;
      closed = true;
      held.revisionListeners.delete(onRevision);
      if (onStatus) held.statusListeners.delete(onStatus);
      if (held.revisionListeners.size === 0 && held.statusListeners.size === 0) {
        held.source.close();
        if (entries.get(url) === held) entries.delete(url);
      }
    },
  };
}

/**
 * Run `task` on a slow interval, but only while the page is visible and `fast` says the
 * event stream is not covering for us. Returns a disposer. Also runs `task` once when the
 * tab becomes visible again, because a hidden tab may have missed events.
 */
export function visiblePoll(
  task: () => void,
  intervalMs: number,
): { reset(ms: number): void; stop(): void } {
  let timer: number | undefined;
  let period = intervalMs;
  const arm = () => {
    window.clearInterval(timer);
    timer = undefined;
    if (document.visibilityState === "visible") timer = window.setInterval(task, period);
  };
  const onVisibility = () => {
    arm();
    if (document.visibilityState === "visible") task();
  };
  document.addEventListener("visibilitychange", onVisibility);
  arm();
  return {
    reset(ms: number) {
      period = ms;
      arm();
    },
    stop() {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    },
  };
}
