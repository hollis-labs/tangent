import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";

import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { TabStrip } from "./components/TabStrip";
import ChannelPane from "./routes/ChannelPane";
import Inbox from "./routes/Inbox";
import Settings from "./routes/Settings";

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
          <Route path="/" element={<Inbox />} />
          <Route path="/inbox" element={<Inbox />} />
          <Route path="/inbox/items/:itemID" element={<Inbox />} />
          <Route path="/channels" element={<ChannelPane />} />
          <Route path="/channels/:channelID" element={<ChannelPane />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/r/:roomID" element={<Inbox />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
