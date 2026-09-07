import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";

import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { TabStrip } from "./components/TabStrip";
import ChannelPane from "./routes/ChannelPane";
import HITLInbox from "./routes/HITLInbox";
import Index from "./routes/Index";
import Room from "./routes/Room";

function Layout() {
  // See RouteErrorBoundary's own doc comment for why this is a plain prop
  // rather than a React key: the boundary itself decides when to clear a
  // crash (any location change), while React's own reconciliation decides
  // whether HITLInbox/ChannelPane/Room's instance continues or is replaced.
  const location = useLocation();
  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <TabStrip />
      <RouteErrorBoundary locationKey={location.pathname}>
        <Outlet />
      </RouteErrorBoundary>
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
          <Route path="/channels" element={<ChannelPane />} />
          <Route path="/channels/:channelID" element={<ChannelPane />} />
          <Route path="/r/:roomID" element={<Room />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
