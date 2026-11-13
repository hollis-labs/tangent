import { IdentityGate } from "@hollis-labs/kit-identity";
import { IdentityProvider } from "@hollis-labs/kit-identity/client";
import { useEffect } from "react";
import { BrowserRouter } from "react-router-dom";

import { UiChannelProvider } from "./hooks/useUiCommands";
import { syncPlugins } from "./lib/plugin-loader";
import { SessionLossBridge } from "./shell/SessionLossBridge";
import { TangentShell } from "./shell/TangentShell";
import { ThemeBoot } from "./shell/ThemeBoot";

// Sync plugins from /api/plugins/registry (CW-20260911-0035 minimal proof).
// Fire-and-forget: plugin load failures should not block the app. It runs once a
// person is signed in (the registry needs a session), after the first render so
// it never queues ahead of the first route's own data requests, and still within
// a second so plugin contributions are not noticeably late.
function PluginSync() {
  useEffect(() => {
    if (typeof window.requestIdleCallback === "function") {
      const handle = window.requestIdleCallback(() => void syncPlugins(), { timeout: 1000 });
      return () => window.cancelIdleCallback(handle);
    }
    const handle = window.setTimeout(() => void syncPlugins(), 0);
    return () => window.clearTimeout(handle);
  }, []);
  return null;
}

function App() {
  return (
    // The identity gate renders setup, sign-in and the no-owner screen outside
    // the shell, and mounts the app only once a session exists.
    <IdentityProvider>
      <ThemeBoot />
      <IdentityGate appName="tangent">
        <PluginSync />
        <SessionLossBridge />
        {/* UI commands acknowledge committed presentation. Declarative router
            transitions would defer the route commit beyond the acknowledgement. */}
        <UiChannelProvider>
          <BrowserRouter useTransitions={false}>
            <TangentShell />
          </BrowserRouter>
        </UiChannelProvider>
      </IdentityGate>
    </IdentityProvider>
  );
}

export default App;
