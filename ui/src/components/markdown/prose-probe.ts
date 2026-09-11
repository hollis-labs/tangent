// Test-only support for the prose-surface assertions. Nothing in the app
// imports this module; it is not a `*.test.ts` file only because it is shared
// across the twenty test files that assert the rule.
//
// The rule: anywhere an agent's own words are displayed, they route through
// <Markdown>. A test of that has to observe a markdown *feature* — a `<strong>`
// that only `**` produces, a `<li>` that only `- ` produces. A snapshot, or an
// assertion on the visible characters, would pass just as happily on a surface
// that printed the literal asterisks, which is the bug.
//
// Each surface takes its own marker so one render can carry a probe in every
// prose field at once and each assertion still names exactly one of them.

import { screen } from "@testing-library/react";
import { expect } from "vitest";

/** Markdown source carrying `marker`-scoped bold and list probes. */
export function proseProbe(marker: string): string {
  return `**${marker}-bold** lead-in\n\n- ${marker}-one\n- ${marker}-two`;
}

/** Assert `proseProbe(marker)` reached the DOM as rendered markdown. */
export function expectProseRendered(marker: string): void {
  expect(screen.getByText(`${marker}-bold`, { selector: "strong" })).toBeInTheDocument();
  expect(screen.getByText(`${marker}-one`, { selector: "li" })).toBeInTheDocument();
  expect(screen.getByText(`${marker}-two`, { selector: "li" })).toBeInTheDocument();
}

/**
 * Assert `proseProbe(marker)` stayed literal inside `container` — the pin on
 * the Leave bucket, so a later sweep cannot quietly markdown-render a diff
 * hunk, a JSON dump, or the exact wording a reviewer is comparing against.
 */
export function expectProseLiteral(container: HTMLElement, marker: string): void {
  expect(container).toHaveTextContent(`**${marker}-bold**`);
  expect(container).toHaveTextContent(`- ${marker}-one`);
  expect(container.querySelector("strong")).toBeNull();
  expect(container.querySelector("li")).toBeNull();
}
