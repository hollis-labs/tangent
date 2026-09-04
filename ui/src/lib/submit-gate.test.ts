import { describe, expect, it, vi } from "vitest";

import { buildSubmitGate, describedBy, focusControl } from "./submit-gate";

describe("buildSubmitGate", () => {
  it("reports an unblocked gate when nothing is outstanding", () => {
    const gate = buildSubmitGate([false, null, undefined]);

    expect(gate.blocked).toBe(false);
    expect(gate.requirements).toEqual([]);
    expect(gate.first).toBeNull();
  });

  it("keeps requirements in the order they were declared", () => {
    const gate = buildSubmitGate([
      false,
      { controlID: "b", label: "second", message: "second remains." },
      null,
      { controlID: "c", label: "third", message: "third remains." },
    ]);

    expect(gate.blocked).toBe(true);
    expect(gate.requirements.map((entry) => entry.controlID)).toEqual(["b", "c"]);
    expect(gate.first?.controlID).toBe("b");
  });
});

describe("describedBy", () => {
  it("omits the attribute entirely when nothing describes the control", () => {
    expect(describedBy(false, null, undefined, "")).toBeUndefined();
  });

  it("joins the ids that are present", () => {
    expect(describedBy("hint", false, "error")).toBe("hint error");
  });
});

describe("focusControl", () => {
  it("scrolls the control into view and focuses it", () => {
    const input = document.createElement("input");
    input.id = "gate-target";
    document.body.append(input);
    const scrollIntoView = vi.fn();
    input.scrollIntoView = scrollIntoView;

    focusControl("gate-target");

    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
    expect(document.activeElement).toBe(input);
    input.remove();
  });

  it("is a no-op when the control is not mounted", () => {
    expect(() => focusControl("gate-missing")).not.toThrow();
  });

  it("still focuses when the environment rejects the preventScroll option", () => {
    const input = document.createElement("input");
    input.id = "gate-strict";
    document.body.append(input);
    const focus = vi
      .fn<(options?: FocusOptions) => void>()
      .mockImplementationOnce(() => {
        throw new TypeError("preventScroll unsupported");
      })
      .mockImplementationOnce(() => {});
    input.focus = focus;

    focusControl("gate-strict");

    expect(focus).toHaveBeenCalledTimes(2);
    expect(focus).toHaveBeenLastCalledWith();
    input.remove();
  });
});
