import { ManifestShell, type PageComponent } from "@hollis-labs/app-shell";
import { IDENTITY_PAGES } from "@hollis-labs/kit-identity";
import {
  Inbox as InboxIcon,
  KeyRound,
  LogOut,
  MessageSquare,
  Monitor,
  Radio,
  Settings as SettingsIcon,
  Shield,
} from "lucide-react";
import { lazy, Suspense } from "react";
import { Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { RouteErrorBoundary } from "@/components/RouteErrorBoundary";
import { manifest } from "./manifest";
import { surfacePath } from "./paths";

// Each destination is its own chunk so the entry stays small; the one being
// navigated to is fetched.
const ChannelPane = lazy(() => import("@/routes/ChannelPane"));
const Chat = lazy(() => import("@/routes/Chat"));
const Inbox = lazy(() => import("@/routes/Inbox"));
const Settings = lazy(() => import("@/routes/Settings"));

// Kit icon names used by the manifest (cmd/tangent/app.ui.json), resolved by the shell.
const icons = {
  inbox: InboxIcon,
  "message-square": MessageSquare,
  radio: Radio,
  settings: SettingsIcon,
  shield: Shield,
  "key-round": KeyRound,
  monitor: Monitor,
  "log-out": LogOut,
};

// Each page is wrapped in the route table its component reads params from (the
// shell is told the destination, the page resolves the deep link) and in a
// boundary that clears when the location changes, so one crashed page does not
// take the shell with it.
function Destination({ children }: { children: React.ReactNode }) {
  const { pathname } = useLocation();
  return (
    <RouteErrorBoundary locationKey={pathname}>
      <Suspense fallback={<p className="p-4 text-sm text-fg-muted">Loading…</p>}>
        {children}
      </Suspense>
    </RouteErrorBoundary>
  );
}

const pages: Record<string, PageComponent> = {
  inbox: () => (
    <Destination>
      <Routes>
        <Route path="/inbox/items/:itemID" element={<Inbox />} />
        <Route path="/r/:roomID" element={<Inbox />} />
        <Route path="*" element={<Inbox />} />
      </Routes>
    </Destination>
  ),
  chat: () => (
    <Destination>
      <Routes>
        <Route path="/chat/:agentID" element={<Chat />} />
        <Route path="*" element={<Chat />} />
      </Routes>
    </Destination>
  ),
  channels: () => (
    <Destination>
      <Routes>
        <Route path="/channels/:channelID" element={<ChannelPane />} />
        <Route path="*" element={<ChannelPane />} />
      </Routes>
    </Destination>
  ),
  settings: () => (
    <Destination>
      <Settings />
    </Destination>
  ),
  "identity-account": () => <IDENTITY_PAGES.account />,
  "identity-keys": () => <IDENTITY_PAGES.keys />,
  "identity-sessions": () => <IDENTITY_PAGES.sessions />,
  "identity-sign-out": () => <IDENTITY_PAGES.signOut />,
};

/** The app-shell, with the browser location bridged to the shell's path and navigate. */
export function TangentShell() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  return (
    <ManifestShell
      manifest={manifest}
      apiBase="/api"
      path={surfacePath(pathname)}
      onNavigate={(to) => navigate(to)}
      pages={pages}
      icons={icons}
    />
  );
}
