import { BrowserRouter, Outlet, Route, Routes } from "react-router-dom";

import { TabStrip } from "./components/TabStrip";
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
          <Route path="/r/:roomID" element={<Room />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
