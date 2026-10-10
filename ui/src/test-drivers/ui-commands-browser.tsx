// Dev-only acceptance entry; it is absent from the production entry/build.
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { UiChannelProvider } from "@/hooks/useUiCommands";
import ChannelPane from "@/routes/ChannelPane";
import Inbox from "@/routes/Inbox";
import { SyntheticUiTransport } from "./synthetic-ui-transport";
import { FixtureEventSource, fixtureFetch } from "./ui-command-fixtures";
import "../index.css";

const transport = new SyntheticUiTransport();
const requests: { path: string; method: string }[] = [];
window.fetch = (input, init) => {
  requests.push({ path: String(input), method: init?.method ?? "GET" });
  return fixtureFetch(input, init);
};
window.EventSource = FixtureEventSource as unknown as typeof EventSource;
transport.onSend = (frame) => {
  if (frame.type === "view.publish") queueMicrotask(() => transport.publish());
};
const fixture = { transport, requests };
Object.assign(window, { uiCommandFixture: fixture });
const root = document.getElementById("root");
if (!root) throw new Error("synthetic fixture root missing");
createRoot(root).render(
  <BrowserRouter useTransitions={false}>
    <UiChannelProvider transport={transport}>
      <Routes>
        <Route path="/channels" element={<ChannelPane />} />
        <Route path="/channels/:channelID" element={<ChannelPane />} />
        <Route path="*" element={<Inbox />} />
      </Routes>
    </UiChannelProvider>
  </BrowserRouter>,
);
