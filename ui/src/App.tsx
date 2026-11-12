import { lazy, Suspense } from "react";
import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";

import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { TabStrip } from "./components/TabStrip";
import { UiChannelProvider } from "./hooks/useUiCommands";

// Each route is its own chunk so the entry stays small; the router fetches the
// one being navigated to.
const ChannelPane = lazy(() => import("./routes/ChannelPane"));
const Chat = lazy(() => import("./routes/Chat"));
const Inbox = lazy(() => import("./routes/Inbox"));
const Settings = lazy(() => import("./routes/Settings"));

function Layout() {
  // See RouteErrorBoundary's own doc comment for why this is a plain prop
  // rather than a React key: the boundary itself decides when to clear a
  // crash (any location change), while React's own reconciliation decides
  // whether HITLInbox/ChannelPane/Room's instance continues or is replaced.
  const location = useLocation();
  return (
    <div className="flex h-screen flex-col bg-bg text-fg">
      <TabStrip />
      <div className="min-h-0 flex-1">
        <RouteErrorBoundary locationKey={location.pathname}>
          <Suspense fallback={<p className="p-4 text-sm text-fg-muted">Loading…</p>}>
            <Outlet />
          </Suspense>
        </RouteErrorBoundary>
      </div>
    </div>
  );
}

function App() {
  return (
    // UI commands acknowledge committed presentation. Declarative router
    // transitions would defer the route commit beyond the acknowledgement.
    <UiChannelProvider>
      <BrowserRouter useTransitions={false}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Inbox />} />
            <Route path="/inbox" element={<Inbox />} />
            <Route path="/inbox/items/:itemID" element={<Inbox />} />
            <Route path="/chat" element={<Chat />} />
            <Route path="/chat/:agentID" element={<Chat />} />
            <Route path="/channels" element={<ChannelPane />} />
            <Route path="/channels/:channelID" element={<ChannelPane />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/r/:roomID" element={<Inbox />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </UiChannelProvider>
  );
}

export default App;
