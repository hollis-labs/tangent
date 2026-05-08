import { BrowserRouter, Route, Routes } from "react-router-dom";

import Room from "./routes/Room";

function Home() {
  return (
    <main className="min-h-screen flex items-center justify-center bg-zinc-950 text-zinc-100">
      <div className="text-center space-y-2">
        <h1 className="text-2xl font-medium tracking-wide">Tangent</h1>
        <p className="text-sm text-zinc-400">v0.1.0-dev</p>
        <p className="text-xs text-zinc-500">
          Open a session at <code>/r/&lt;roomID&gt;</code>.
        </p>
      </div>
    </main>
  );
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/r/:roomID" element={<Room />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
