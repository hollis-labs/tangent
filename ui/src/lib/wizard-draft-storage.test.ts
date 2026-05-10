import { describe, expect, it, vi } from "vitest";

import { clearWizardDraft, saveWizardDraft, type WizardDraftRecord } from "./wizard-draft-storage";

describe("wizard draft storage", () => {
  it("swallows localStorage set failures", () => {
    const setItem = vi.spyOn(window.localStorage.__proto__, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });
    const record: WizardDraftRecord = {
      version: 1,
      roomID: "room-1",
      wizardID: "wizard-1",
      envelopeId: "env-1",
      baseSeedKey: "seed",
      currentStepID: "step-1",
      progress: [],
      branchSelections: [],
      savedAt: "2026-05-10T00:00:00.000Z",
    };

    expect(() => saveWizardDraft(record)).not.toThrow();
    setItem.mockRestore();
  });

  it("swallows localStorage remove failures", () => {
    const removeItem = vi
      .spyOn(window.localStorage.__proto__, "removeItem")
      .mockImplementation(() => {
        throw new Error("storage blocked");
      });

    expect(() => clearWizardDraft("room-1", "wizard-1")).not.toThrow();
    removeItem.mockRestore();
  });
});
