import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeBoot } from "./ThemeBoot";

const setMode = vi.hoisted(() => vi.fn());
vi.mock("@hollis-labs/design-tokens", () => ({ setMode }));

function media(matches: boolean) {
  const listeners = new Set<() => void>();
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches,
      addEventListener: (_: string, fn: () => void) => listeners.add(fn),
      removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
    })),
  );
  return listeners;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  setMode.mockClear();
});

describe("ThemeBoot", () => {
  it("applies the stored preference at start", () => {
    media(true);
    window.localStorage.setItem("tangent.theme-preference", "dark");
    render(<ThemeBoot />);
    expect(setMode).toHaveBeenLastCalledWith("dark");
  });

  it("follows the system when nothing is stored, and a later explicit choice wins over a system change", () => {
    const listeners = media(true);
    render(<ThemeBoot />);
    expect(setMode).toHaveBeenLastCalledWith("light");
    window.localStorage.setItem("tangent.theme-preference", "dark");
    for (const fn of listeners) fn();
    expect(setMode).toHaveBeenLastCalledWith("dark");
  });
});
