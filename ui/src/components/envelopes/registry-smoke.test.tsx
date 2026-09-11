// Registry-linked smoke coverage for the shipped room workflows.
//
// The two halves do different jobs.
//
// The first half reads BOTH production registries off disk — the Go envelope
// extensions and the SPA's boot-time `register()` calls — and fails if they
// disagree. A workflow that ships on one side only is otherwise invisible: the
// server accepts the envelope and the browser falls through to the JSON debug
// renderer, which looks like a bug in the workflow rather than a missing
// registration. Driving the inventory from the registry is also what keeps an
// affordance audit honest: a new workflow cannot quietly escape it.
//
// The second half renders one representative of each workflow family through
// the real registry and router, and checks the affordance contract end to end.

import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { _resetRegistryForTests, lookup, register } from "@/lib/envelope-registry";

import { ApprovalQueue, type ApprovalQueueEnvelope } from "./ApprovalQueue";
import { EnvelopeRouter } from "./EnvelopeRouter";
import { FormCollect, type FormCollectEnvelope } from "./FormCollect";
import { InterviewQuestion, type InterviewQuestionEnvelope } from "./InterviewQuestion";
import { Wizard, type WizardEnvelope } from "./Wizard";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(HERE, "..", "..", "..", "..");
const EXTENSIONS_DIR = join(REPO_ROOT, "internal", "envelope", "extensions");
const MAIN_TSX = join(REPO_ROOT, "ui", "src", "main.tsx");

/**
 * Workflows with no terminal submit gate, and why. Each entry is a deliberate
 * decision recorded in docs/room-validation-affordances.md — inventing a gate
 * for one of these would change workflow semantics rather than polish an
 * affordance. Anything NOT listed here must render a SubmitGateNotice.
 */
const NO_TERMINAL_GATE: Record<string, string> = {
  "tangent.synthesis-notes": "read-only acknowledgement; no editable controls",
  "tangent.output-render": "read-only acknowledgement; no editable controls",
  "tangent.dashboard": "submitting an unchanged dashboard is a legitimate outcome",
  "tangent.spreadsheet-review": "submitting an empty review is a legitimate outcome",
  // The board's terminal actions are the caller's own, with no required input
  // in front of them: there is nothing the participant could fail to fill in,
  // so there is no gate for a notice to explain. Its non-terminal state goes to
  // a draft instead, which never blocks and is never a submission.
  "tangent.app-board": "caller-supplied actions with no required input; view state is a draft",
};

function goEnvelopeTypes(): string[] {
  const types: string[] = [];
  for (const entry of readdirSync(EXTENSIONS_DIR)) {
    if (!entry.endsWith(".go") || entry.endsWith("_test.go")) {
      continue;
    }
    const source = readFileSync(join(EXTENSIONS_DIR, entry), "utf8");
    for (const match of source.matchAll(/EnvelopeType\s*=\s*"(tangent\.[a-z-]+)"/g)) {
      types.push(match[1]);
    }
  }
  return types;
}

function registeredEnvelopeTypes(): string[] {
  const source = readFileSync(MAIN_TSX, "utf8");
  return [...source.matchAll(/register\(\s*"(tangent\.[a-z-]+)"/g)].map((match) => match[1]);
}

/** `tangent.approval-queue` → `ApprovalQueue.tsx`. */
function componentFileFor(envelopeType: string): string {
  const name = envelopeType
    .replace(/^tangent\./, "")
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join("");
  return join(HERE, `${name}.tsx`);
}

describe("shipped room workflow registry", () => {
  const goTypes = goEnvelopeTypes();
  const spaTypes = registeredEnvelopeTypes();

  it("registers every server-side envelope extension in the SPA", () => {
    // hitl-item is the global /hitl ledger, not a room workflow: it has no
    // room presentation and is deliberately absent from the SPA registry.
    const roomWorkflows = goTypes.filter((type) => type !== "tangent.hitl-item");
    expect([...roomWorkflows].sort()).toEqual([...spaTypes].sort());
  });

  it("covers every Tangent-owned room workflow exactly once", () => {
    // The count is derived from the Go side rather than typed here. It moved
    // the first time a workflow was added after this test was written, and a
    // literal that has to be edited alongside the thing it checks is not a
    // check. `hitl-item` is excluded for the reason given above.
    const expected = goTypes.filter((type) => type !== "tangent.hitl-item").length;
    expect(new Set(spaTypes).size).toBe(spaTypes.length);
    expect(spaTypes).toHaveLength(expected);
  });

  it.each(registeredEnvelopeTypes())(
    "%s ships a component with a terminal-CTA affordance",
    (envelopeType) => {
      const source = readFileSync(componentFileFor(envelopeType), "utf8");
      if (envelopeType in NO_TERMINAL_GATE) {
        expect(source).not.toContain("SubmitGateNotice");
        return;
      }
      // The notice is the only sanctioned way to explain a blocked terminal
      // CTA. A workflow that grows a gate without one is the exact regression
      // this task existed to remove.
      expect(source).toContain("SubmitGateNotice");
      expect(source).toContain("buildSubmitGate");
    },
  );

  it.each(registeredEnvelopeTypes())(
    "%s never marks a requirement by placeholder alone",
    (type) => {
      const source = readFileSync(componentFileFor(type), "utf8");
      // "Required when …" in a placeholder is precisely how the approval queue
      // hid its defer reason: it vanishes on the first keystroke and no screen
      // reader treats it as a requirement.
      expect(source).not.toMatch(/placeholder=\{?["'][^"']*[Rr]equired/);
    },
  );
});

describe("representative workflow families", () => {
  beforeEach(() => {
    _resetRegistryForTests();
    window.localStorage.clear();
  });

  it("dispatches a simple terminal form through the production router", () => {
    register("tangent.interview-question", ({ envelope, onSubmit, onCancel }) => (
      <InterviewQuestion
        envelope={envelope as InterviewQuestionEnvelope}
        onSubmit={onSubmit as never}
        onCancel={onCancel}
      />
    ));
    expect(lookup("tangent.interview-question")).not.toBeNull();

    render(
      <EnvelopeRouter
        envelope={{
          v: 1,
          id: "iq-1",
          type: "tangent.interview-question",
          title: "One question",
          data: { question: "What changed?" },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.queryByTestId("envelope-router-fallback")).not.toBeInTheDocument();
    expect(screen.getByTestId("interview-question-submit")).toBeDisabled();
    expect(screen.getByTestId("interview-question-submit-gate")).toBeInTheDocument();
  });

  it("dispatches a batch/queue workflow and names the outstanding item", () => {
    register("tangent.approval-queue", ({ envelope, onSubmit, onCancel, roomID }) => (
      <ApprovalQueue
        envelope={envelope as ApprovalQueueEnvelope}
        onSubmit={onSubmit as never}
        onCancel={onCancel}
        roomID={roomID}
      />
    ));

    render(
      <EnvelopeRouter
        envelope={{
          v: 1,
          id: "aq-1",
          type: "tangent.approval-queue",
          data: {
            queue_id: "queue-smoke",
            items: [{ id: "only", title: "Only item", summary: "s" }],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-smoke"
      />,
    );

    expect(screen.queryByTestId("envelope-router-fallback")).not.toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-submit")).toBeDisabled();
    expect(screen.getByTestId("approval-queue-submit-gate-reason")).toHaveTextContent(
      "1 item still needs a decision",
    );
  });

  it("dispatches a conditional multi-step flow", () => {
    register("tangent.wizard", ({ envelope, onSubmit, onCancel, roomID }) => (
      <Wizard
        envelope={envelope as WizardEnvelope}
        onSubmit={onSubmit as never}
        onCancel={onCancel}
        roomID={roomID}
      />
    ));

    render(
      <EnvelopeRouter
        envelope={{
          v: 1,
          id: "wz-1",
          type: "tangent.wizard",
          data: {
            wizard_id: "wizard-smoke",
            current_step_id: "step-1",
            steps: [
              {
                step_id: "step-1",
                title: "First",
                fields: [{ id: "why", label: "Why", kind: "text", required: true }],
              },
              { step_id: "step-2", title: "Second", fields: [] },
            ],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-smoke"
      />,
    );

    expect(screen.queryByTestId("envelope-router-fallback")).not.toBeInTheDocument();
    expect(screen.getByTestId("wizard-submit-gate")).toBeInTheDocument();
  });

  it("dispatches a draft-bearing workflow", () => {
    register("tangent.form-collect", ({ envelope, onSubmit, onCancel, roomID }) => (
      <FormCollect
        envelope={envelope as FormCollectEnvelope}
        onSubmit={onSubmit as never}
        onCancel={onCancel}
        roomID={roomID}
      />
    ));

    render(
      <EnvelopeRouter
        envelope={{
          v: 1,
          id: "fc-1",
          type: "tangent.form-collect",
          data: {
            form_id: "form-smoke",
            schema: { fields: [{ id: "why", label: "Why", type: "text", required: true }] },
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-smoke"
      />,
    );

    expect(screen.queryByTestId("envelope-router-fallback")).not.toBeInTheDocument();
    expect(screen.getByTestId("form-collect-submit")).toBeDisabled();
    expect(screen.getByTestId("form-collect-submit-gate")).toBeInTheDocument();
  });

  it("falls back visibly for an unregistered type rather than rendering nothing", () => {
    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "x", type: "tangent.not-a-workflow" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId("envelope-router-fallback")).toBeInTheDocument();
  });
});
