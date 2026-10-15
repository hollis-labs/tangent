import { describe, expect, it } from "vitest";
import { createCoalescer } from "./coalesce";

describe("createCoalescer", () => {
  it("folds calls made mid-run into one follow-up run", async () => {
    let runs = 0;
    let release: () => void = () => {};
    const run = createCoalescer(async () => {
      runs++;
      if (runs === 1) await new Promise<void>((resolve) => (release = resolve));
    });
    const first = run();
    void run();
    void run();
    release();
    await first;
    expect(runs).toBe(2);
    await run();
    expect(runs).toBe(3);
  });
});
