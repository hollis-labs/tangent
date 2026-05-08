import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import { Triage, type TriageEnvelope, type TriageResponse } from "./components/envelopes/Triage";
import { type EnvelopeComponentProps, register } from "./lib/envelope-registry";
import "./index.css";

// v0.1 envelope component registrations. Each component registers
// against the canonical wire `type` string. tangent.triage carries the
// "tangent." plugin prefix because go-envelopes reserves bare names
// for core types (see internal/envelope/extensions/triage.go for the
// matching server-side registration).
//
// The registry is intentionally module-scoped and registered at app
// boot — auto-registration from codegen is a v0.3 concern (one
// component is not enough scaffolding to justify the abstraction).

function TriageAdapter({ envelope, onSubmit, onCancel }: EnvelopeComponentProps) {
  // The registry erases the per-component envelope shape on the way
  // through the type → component map. This adapter narrows back to
  // TriageEnvelope at the boundary so the component itself stays
  // strictly typed.
  return (
    <Triage
      envelope={envelope as TriageEnvelope}
      onSubmit={onSubmit as (response: TriageResponse) => void}
      onCancel={onCancel}
    />
  );
}

register("tangent.triage", TriageAdapter);

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("Tangent: #root not found in index.html");
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
