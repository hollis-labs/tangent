/**
 * Checks that the derived values in src/styles/tangent-theme.css still agree
 * with the derivation functions @hollis-labs/design-tokens exports (R1-R4).
 *
 * From 0.2 the package exports the rules (deriveMuted, deriveHover, ...), so
 * this reads the 18 authored Tangent values back out of the generated file,
 * derives the other values with the package's own functions, and compares. It
 * also fails if the file is stale against gen-tangent-theme.mjs. A failure
 * after a design-tokens upgrade means the rules moved and the theme needs
 * regenerating (`npm run theme:gen`) — it does not mean this script is wrong.
 *
 * Tangent's foreground policy is deliberate: X-fg is white or the theme bg,
 * whichever contrasts more (the package built-ins use black/white).
 *
 * Run: npm run theme:verify
 */
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  deriveActive,
  deriveForeground,
  deriveHover,
  deriveMuted,
  deriveSurfaceActive,
  deriveSyntax,
} from "@hollis-labs/design-tokens";

const themePath = fileURLToPath(new URL("../src/styles/tangent-theme.css", import.meta.url));
const css = readFileSync(themePath, "utf8");
const t = Object.fromEntries([...css.matchAll(/--hl-([\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
const WHITE = "rgb(255 255 255)";

let checked = 0;
const failures = [];
const expect = (label, derived) => {
  checked++;
  const [name] = label.split(" ");
  if (derived !== t[name]) failures.push(`${label}\n     derived: ${derived}\n     in file: ${t[name]}`);
};

for (const base of ["primary", "brand", "danger"]) {
  if (t[`${base}-hover`]) expect(`${base}-hover (R2)`, deriveHover(t[base]));
  if (t[`${base}-active`]) expect(`${base}-active (R2)`, deriveActive(t[base]));
}
for (const base of ["primary", "brand", "danger", "warning", "success", "info"]) {
  expect(`${base}-muted (R1)`, deriveMuted(t[base], 0.12, "rgb"));
  expect(`${base}-fg (R3)`, deriveForeground(t[base], t.bg, WHITE));
}
expect("surface-active (R2)", deriveSurfaceActive(t["surface-hover"], t.fg));
const syn = deriveSyntax(t.fg, t["fg-faint"], "rgb");
for (const [k, v] of Object.entries(syn)) expect(`${k} (R4)`, v);

checked++;
const fresh = execFileSync("node", [fileURLToPath(new URL("./gen-tangent-theme.mjs", import.meta.url))], {
  encoding: "utf8",
});
const strip = (s) => s.replace(/\s+/g, "");
if (strip(fresh) !== strip(css)) failures.push("tangent-theme.css is stale against gen-tangent-theme.mjs (run npm run theme:gen)");

if (failures.length) {
  console.error(`FAIL - ${failures.length} of ${checked} checks failed:\n`);
  for (const f of failures) console.error(`  - ${f}\n`);
  process.exit(1);
}
console.log(`OK - ${checked} checks: derived values match @hollis-labs/design-tokens' exported rules and the file is current.`);
