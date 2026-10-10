import {
  createContext,
  type ReactNode,
  type RefObject,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { flushSync } from "react-dom";
import { useLocation } from "react-router-dom";
import {
  type UiCommand,
  type UiHandler,
  type UiResult,
  type UiTransport,
  type ViewDescriptor,
  validDescriptor,
} from "@/lib/ui-channel";

type ChannelContext = {
  transport: UiTransport | null;
  commandOrigin: RefObject<boolean>;
  attach: (transport: UiTransport) => () => void;
};
// Publication correlation survives route-hook replacement on the same attachment.
type Publication = { signature: string; owner: object };
type Publications = {
  owner: object | null;
  pending: Publication | null;
  acknowledged: (Publication & { revision: number }) | null;
};
const publications = new WeakMap<UiTransport, Publications>();
function publicationQueue(transport: UiTransport): Publications {
  let state = publications.get(transport);
  if (state) return state;
  state = { owner: null, pending: null, acknowledged: null };
  const queue = state;
  publications.set(transport, queue);
  // Correlation belongs to the attachment, including gaps with no adopting route.
  // The listener has the transport's lifetime; pending correlation is transient, with no route handlers.
  transport.subscribe((event) => {
    if (event.type === "view.published" && queue.pending) {
      queue.acknowledged = { ...queue.pending, revision: event.view_revision };
      queue.pending = null;
    } else if (event.type === "attached" || event.type === "detached" || event.type === "refused") {
      queue.pending = null;
      queue.acknowledged = null;
    }
  });
  return queue;
}
const Channel = createContext<ChannelContext>({
  transport: null,
  commandOrigin: { current: false },
  attach: () => () => {},
});
/** The host must supply a real room attachment; this provider creates no connection or identity. */
export function UiChannelProvider({
  children,
  transport: injected,
}: {
  children: ReactNode;
  transport?: UiTransport;
}) {
  const commandOrigin = useRef(false);
  useEffect(() => {
    const participantNavigation = () => {
      commandOrigin.current = false;
    };
    window.addEventListener("pointerdown", participantNavigation);
    window.addEventListener("keydown", participantNavigation);
    window.addEventListener("popstate", participantNavigation);
    return () => {
      window.removeEventListener("pointerdown", participantNavigation);
      window.removeEventListener("keydown", participantNavigation);
      window.removeEventListener("popstate", participantNavigation);
    };
  }, []);
  const [attached, setAttached] = useState<UiTransport | null>(null);
  const attach = useRef((transport: UiTransport) => {
    setAttached(transport);
    return () => setAttached((current) => (current === transport ? null : current));
  }).current;
  return (
    <Channel.Provider value={{ transport: injected ?? attached, attach, commandOrigin }}>
      {children}
    </Channel.Provider>
  );
}
export function useUiChannelAttachment() {
  return useContext(Channel).attach;
}

export function useUiCommandOrigin() {
  return useContext(Channel).commandOrigin;
}

/** Accept only declared, validated synchronous presentation handlers, then commit before ack. */
export function useUiCommands(handlers: UiHandler[]) {
  const commandOrigin = useUiCommandOrigin();
  return {
    declarations: handlers.map((handler) => handler.declaration),
    apply: (event: UiCommand): UiResult => {
      const handler = handlers.find((candidate) => candidate.declaration.name === event.name);
      const before = window.location.href;
      const push = window.history.pushState,
        replace = window.history.replaceState;
      const outcome = { status: "rejected" as UiResult };
      const previousOrigin = commandOrigin.current;
      try {
        if (
          !handler ||
          handler.declaration.scope !== event.scope ||
          !handler.validate(event.arguments) ||
          new TextEncoder().encode(JSON.stringify(event.arguments)).length > 8192
        )
          return outcome.status;
        if (event.scope === "ephemeral") {
          const refuseHistory = () => {
            throw new Error("ephemeral history change refused");
          };
          window.history.pushState = refuseHistory;
          window.history.replaceState = refuseHistory;
        }
        if (event.scope === "url-backed") commandOrigin.current = true;
        flushSync(() => {
          outcome.status = handler.apply(event.arguments);
        });
        if (event.scope === "ephemeral" && window.location.href !== before)
          outcome.status = "rejected";
      } catch {
        outcome.status = "rejected";
      } finally {
        window.history.pushState = push;
        window.history.replaceState = replace;
      }
      if (outcome.status !== "applied") commandOrigin.current = previousOrigin;
      return outcome.status;
    },
  };
}

export function useViewDescriptor(
  observation: Omit<ViewDescriptor, "version" | "route" | "available_commands">,
  commands: ReturnType<typeof useUiCommands>,
  viewKey?: string,
) {
  const { transport } = useContext(Channel);
  const location = useLocation();
  const descriptor: ViewDescriptor = {
    version: 1,
    route: location.pathname,
    ...observation,
    available_commands: commands.declarations,
  };
  // Local-only state can fence commands without disclosing its value to the host.
  const signature = JSON.stringify([descriptor, viewKey]);
  const current = useRef({ descriptor, commands, signature });
  const revision = useRef<number | null>(null);
  const lastRevision = useRef<number | null>(null);

  const published = useRef<string | null>(null);
  const enabledRef = useRef(false);
  const [enabled, setEnabled] = useState(false);
  const [status, setStatus] = useState("unavailable");
  const seen = useRef(new Set<string>());
  const publish = useRef<() => void>(() => {});

  useLayoutEffect(() => {
    current.current = { descriptor, commands, signature };
    revision.current =
      signature === published.current && !(transport && publications.get(transport)?.pending)
        ? lastRevision.current
        : null;
  });

  useLayoutEffect(() => {
    revision.current = null;
    published.current = null;
    seen.current.clear();
    enabledRef.current = false;
    setEnabled(false);
    setStatus(transport?.connected() ? "awaiting view" : "unavailable");
    if (!transport) return;
    const queue = publicationQueue(transport);
    const owner = {};
    queue.owner = owner;
    let applyingCommand = false;
    let refused = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let alive = true;
    const schedule = () => {
      if (refused || timer !== undefined || queue.pending || !transport.connected()) return;
      timer = setTimeout(() => {
        timer = undefined;
        if (
          !alive ||
          refused ||
          !transport.connected() ||
          queue.pending ||
          current.current.signature === published.current
        )
          return;
        if (!validDescriptor(current.current.descriptor)) {
          revision.current = null;
          transport.send({ type: "view.active", active: false });
          setStatus("observation exceeds bounds");
          return;
        }
        queue.pending = { signature: current.current.signature, owner };
        if (!transport.send({ type: "view.publish", descriptor: current.current.descriptor })) {
          queue.pending = null;
          setStatus("unavailable");
        }
      }, 110);
    };
    publish.current = schedule;
    const active = () => {
      if (refused || !transport.connected()) return;
      const foreground = document.visibilityState !== "hidden" && document.hasFocus();
      transport.send({ type: "view.active", active: foreground });
      if (!foreground) revision.current = null;
      else if (published.current === current.current.signature)
        revision.current = lastRevision.current;
    };
    const reset = () => {
      revision.current = null;
      lastRevision.current = null;
      queue.pending = null;
      published.current = null;
      enabledRef.current = false;
      setEnabled(false);
      seen.current.clear();
    };
    const unsubscribe = transport.subscribe((event) => {
      if (!alive) return;
      if (event.type === "attached") {
        refused = false;
        reset();
        setStatus("awaiting view");
        active();
        schedule();
      } else if (event.type === "detached") {
        reset();
        setStatus("unavailable");
      } else if (event.type === "refused") {
        refused = true;
        reset();
        setStatus("host refused channel");
      } else if (event.type === "view.published") {
        // Only one publication is outstanding: the v1 acknowledgement has no request id.
        if (!queue.acknowledged) return;
        const received = queue.acknowledged;
        queue.acknowledged = null;
        if (received.owner !== owner) {
          schedule();
          return;
        }
        published.current = received.signature;
        if (lastRevision.current !== event.view_revision) seen.current.clear();
        lastRevision.current = event.view_revision;
        if (published.current === current.current.signature) {
          revision.current = event.view_revision;
          setStatus("ready");
        } else schedule();
      } else if (event.type === "ui.command") {
        let result: "applied" | "not_visible" | "rejected" = "rejected";
        const accepted =
          enabledRef.current &&
          document.visibilityState !== "hidden" &&
          document.hasFocus() &&
          revision.current === event.view_revision &&
          published.current === current.current.signature &&
          !seen.current.has(event.command_id) &&
          seen.current.size < 256;
        if (accepted) {
          seen.current.add(event.command_id);
          applyingCommand = true;
          result = current.current.commands.apply(event);
        }
        transport.send({
          type: "ui.ack",
          ack: { command_id: event.command_id, view_revision: event.view_revision, status: result },
        });
        applyingCommand = false;
      }
    });
    document.addEventListener("visibilitychange", active);
    window.addEventListener("focus", active);
    window.addEventListener("blur", active);
    window.addEventListener("pointerdown", active);
    window.addEventListener("keydown", active);
    if (transport.connected()) {
      active();
      schedule();
    }
    return () => {
      alive = false;
      if (timer !== undefined) clearTimeout(timer);
      publish.current = () => {};
      unsubscribe();
      document.removeEventListener("visibilitychange", active);
      window.removeEventListener("focus", active);
      window.removeEventListener("blur", active);
      window.removeEventListener("pointerdown", active);
      window.removeEventListener("keydown", active);
      const retire = () => {
        transport.send({ type: "ui.control", enabled: false });
        if (queue.owner === owner) {
          transport.send({ type: "view.active", active: false });
          queue.owner = null;
        }
      };
      // Router application may unmount this hook inside flushSync. Ack must precede
      // disabling/retiring the old view, which cancels the host's pending command.
      if (applyingCommand) queueMicrotask(retire);
      else retire();
      revision.current = null;
    };
  }, [transport]);
  useEffect(() => {
    if (signature !== published.current) publish.current();
  }, [signature]);
  return {
    enabled,
    status,
    setEnabled: (value: boolean) => {
      if (!transport?.connected() || status !== "ready") return;
      if (!transport.send({ type: "ui.control", enabled: value })) return;
      enabledRef.current = value;
      setEnabled(value);
    },
  };
}

export function UiControl({ control }: { control: ReturnType<typeof useViewDescriptor> }) {
  return (
    <label className="inline-flex items-center gap-2 text-xs text-fg-muted">
      <input
        type="checkbox"
        checked={control.enabled}
        disabled={control.status !== "ready"}
        onChange={(event) => control.setEnabled(event.target.checked)}
      />
      Allow agent view control <span role="status">{control.status}</span>
    </label>
  );
}
