import { describe, expect, it } from "vitest";
import { navigateCommand, validDescriptor } from "./ui-channel";

describe("view observation bounds", () => {
  it("accepts minimal carrier, rejects overflow, duplicates, invalid route and Unicode IDs", () => {
    expect(validDescriptor({ version: 1, route: "/inbox" })).toBe(true);
    expect(validDescriptor({ version: 1, route: "/inbox?secret=yes" })).toBe(false);
    expect(validDescriptor({ version: 1, route: "/inbox", search: "é".repeat(513) })).toBe(false);
    expect(validDescriptor({ version: 1, route: "/inbox", selected_ids: ["a", "a"] })).toBe(false);
    expect(validDescriptor({ version: 1, route: "/inbox", selected_ids: ["a\u200bb"] })).toBe(
      false,
    );
    expect(
      validDescriptor({
        version: 1,
        route: "/inbox",
        visible_rows: Array.from({ length: 33 }, (_, i) => ({ id: `row-${i}`, summary: "Item" })),
      }),
    ).toBe(false);
  });
  it("permits display newline/tab but refuses control bytes and lone surrogates", () => {
    expect(validDescriptor({ version: 1, route: "/docs", search: "line\n\ttext" })).toBe(true);
    expect(validDescriptor({ version: 1, route: "/docs", search: "\u0000" })).toBe(false);
    expect(validDescriptor({ version: 1, route: "/docs", search: "\ud800" })).toBe(false);
  });
  it("allows local router targets and refuses cross-origin and extra arguments", () => {
    const command = navigateCommand(() => {});
    expect(command.validate({ route: "/inbox?view=history#item" })).toBe(true);
    for (const route of [
      "//example.test",
      "/\\example.test",
      "https://example.test",
      "/bad\npath",
      "/unsupported",
      "/channels/a/extra",
    ])
      expect(command.validate({ route })).toBe(false);
    expect(command.validate({ route: "/", extra: true })).toBe(false);
  });
});
