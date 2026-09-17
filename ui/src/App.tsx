import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";

import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { TabStrip } from "./components/TabStrip";
import ChannelPane from "./routes/ChannelPane";
import DocsInbox from "./routes/DocsInbox";
import HITLInbox from "./routes/HITLInbox";
import Index from "./routes/Index";
import Room from "./routes/Room";
import Settings from "./routes/Settings";
import TurnsInbox from "./routes/TurnsInbox";

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
          <Outlet />
        </RouteErrorBoundary>
      </div>
    </div>
  );
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Index />} />
          <Route path="/hitl" element={<HITLInbox />} />
          <Route path="/hitl/items/:itemID" element={<HITLInbox />} />
          <Route path="/turns" element={<TurnsInbox />} />
          <Route path="/turns/items/:itemID" element={<TurnsInbox />} />
          <Route path="/docs" element={<DocsInbox />} />
          <Route path="/docs/items/:itemID" element={<DocsInbox />} />
          <Route path="/channels" element={<ChannelPane />} />
          <Route path="/channels/:channelID" element={<ChannelPane />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/r/:roomID" element={<Room />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
