import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";

import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { TabStrip } from "./components/TabStrip";
import ChannelPane from "./routes/ChannelPane";
import HITLInbox from "./routes/HITLInbox";
import Index from "./routes/Index";
import Room from "./routes/Room";

function Layout() {
  // Keyed by the top-level section ("hitl", "channels", "r", "" for the
  // index), not the full resolved path: HITLInbox and ChannelPane are each
  // one long-lived component instance that reacts to its own dynamic param
  // (itemID / channelID) changing via useParams, the same way Room reacts to
  // roomID — keying on the full path would remount that instance on every
  // in-page selection, destroying state a route was never meant to lose
  // (confirmed the hard way: it broke HITLInbox's own focus-management
  // continuity between items). A fresh boundary only when the operator
  // actually leaves for a different top-level page is what
  // CW-20260907-0085 asked for; resetting a still-crashed page you haven't
  // left is what the "Try again" button is for.
  const location = useLocation();
  const section = location.pathname.split("/")[1] ?? "";
  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <TabStrip />
      <RouteErrorBoundary key={section}>
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
