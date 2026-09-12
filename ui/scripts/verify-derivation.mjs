/**
 * Checks that the derivation rules in gen-tangent-theme.mjs still reproduce
 * @hollis-labs/design-tokens' OWN shipped palettes, byte for byte.
 *
 * The rules are re-implemented from that package's README prose because the
 * package exports the derived values but not the rules. Prose is not a contract,
 * so this replays them against palettes the package derived itself. If this
 * fails after a design-tokens upgrade, the rules moved and
 * `src/styles/tangent-theme.css` needs regenerating — it does not mean this
 * script is wrong.
 *
 * Run: npm run theme:verify
 */
import { getBuiltinTheme } from "@hollis-labs/design-tokens";

const parse = (s) => {
  const m = s.match(/rgb\((\d+) (\d+) (\d+)(?: \/ (\d+)%)?\)/);
  if (!m) throw new Error(`unparseable colour: ${s}`);
  return { c: [+m[1], +m[2], +m[3]], a: m[4] ? +m[4] : null };
};
const mix = (a, b, t) => a.map((c, i) => Math.round(c + (b[i] - c) * t));
const rgb = ([r, g, b]) => `rgb(${r} ${g} ${b})`;
const WHITE = [255, 255, 255];
const BLACK = [0, 0, 0];

// Palettes the package's README says were filled entirely by R1-R3.
const SUBJECTS = ["sysop-p4-white", "sysop-green-phosphor", "sysop-amber-phosphor", "sysop-hi-contrast"];

let checked = 0;
const failures = [];
const expect = (label, got, want) => {
  checked++;
  if (got !== want) failures.push(`${label}\n     derived: ${got}\n     shipped: ${want}`);
};

for (const id of SUBJECTS) {
  const t = getBuiltinTheme(id).tokens.dark;
  const fg = parse(t.fg).c;

  for (const base of ["primary", "brand", "danger"]) {
    const c = parse(t[base]).c;
    // R1 - X-muted is X at 12% alpha.
    const muted = t[`${base}-muted`];
    if (muted) {
      const p = parse(muted);
      expect(`${id} ${base}-muted (R1)`, `rgb(${c.join(" ")} / 12%)`, `rgb(${p.c.join(" ")} / ${p.a}%)`);
    }
    // R2 - -hover is 12% toward white, -active 12% toward black.
    if (t[`${base}-hover`]) expect(`${id} ${base}-hover (R2)`, rgb(mix(c, WHITE, 0.12)), t[`${base}-hover`]);
    if (t[`${base}-active`]) expect(`${id} ${base}-active (R2)`, rgb(mix(c, BLACK, 0.12)), t[`${base}-active`]);
  }

  // R2 (surface) - surface-active is surface-hover 15% toward fg.
  if (t["surface-active"] && t["surface-hover"]) {
    expect(`${id} surface-active (R2)`, rgb(mix(parse(t["surface-hover"]).c, fg, 0.15)), t["surface-active"]);
  }
}

if (failures.length) {
  console.error(`FAIL - ${failures.length} of ${checked} derivations no longer reproduce the package:\n`);
  for (const f of failures) console.error(`  - ${f}\n`);
  process.exit(1);
}
console.log(`OK - all ${checked} derivations reproduce @hollis-labs/design-tokens' own palettes exactly.`);
