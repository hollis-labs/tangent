import { BrowserRouter, Outlet, Route, Routes } from "react-router-dom";

import { TabStrip } from "./components/TabStrip";
import ChannelPane from "./routes/ChannelPane";
import HITLInbox from "./routes/HITLInbox";
import Index from "./routes/Index";
import Room from "./routes/Room";

function Layout() {
  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <TabStrip />
      <Outlet />
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
