/**
 * Generates the Tangent value layer (`--hl-*`) for the design-kit token contract.
 *
 * Why this script exists, rather than 48 hand-written hex values: Tangent's own
 * palette only answers 18 of the contract's 48 colour names. The other 30 are
 * filled by the same four rules @hollis-labs/design-tokens used to fill the 168
 * values nobody chose (its README, "The 168 values nobody chose").
 *
 * The package exports those values as data (DERIVED_TOKEN_VALUES) but does not
 * export the rules as functions, so they are re-implemented here. The
 * implementation is not trusted on the prose — `npm run theme:verify` replays it
 * against the package's own shipped `sysop-p4-white` palette and fails if it
 * stops reproducing it byte for byte. That is what makes these derivations
 * checkable instead of asserted.
 *
 * Run: node scripts/gen-tangent-theme.mjs > src/styles/tangent-theme.css
 */

const hex = (h) => {
  const s = h.replace("#", "");
  return [0, 2, 4].map((i) => Number.parseInt(s.slice(i, i + 2), 16));
};
const rgb = ([r, g, b]) => `rgb(${r} ${g} ${b})`;
const alpha = ([r, g, b], pct) => `rgb(${r} ${g} ${b} / ${pct}%)`;

/** Linear sRGB interpolation, rounded. Verified against the package's output. */
const mix = (a, b, t) => a.map((c, i) => Math.round(c + (b[i] - c) * t));

const WHITE = [255, 255, 255];
const BLACK = [0, 0, 0];

/** R1 — `X-muted` is X at 12% alpha. The contract's own definition (§3.8 rule 3). */
const r1Muted = (c) => alpha(c, 12);
/** R2 — `-hover` is 12% toward white, `-active` 12% toward black. */
const r2Hover = (c) => rgb(mix(c, WHITE, 0.12));
const r2Active = (c) => rgb(mix(c, BLACK, 0.12));
/** R2 (surface) — `surface-active` is `surface-hover` 15% toward `fg`. */
const r2SurfaceActive = (surfaceHover, fg) => rgb(mix(surfaceHover, fg, 0.15));

/** WCAG relative luminance, for R3. */
const lum = ([r, g, b]) =>
  [r, g, b]
    .map((v) => v / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
    .reduce((acc, v, i) => acc + v * [0.2126, 0.7152, 0.0722][i], 0);
const contrast = (a, b) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};
/** R3 — `X-fg` is white or the theme's own bg, whichever contrasts more. */
const r3Fg = (c, bg) => rgb(contrast(c, WHITE) >= contrast(c, bg) ? WHITE : bg);

/** R4 — `syntax-*` is five stops at 100/75/50/25/0 between `fg` and `fg-faint`, in oklab. */
const srgbToLinear = (v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
const linearToSrgb = (v) => (v <= 0.0031308 ? v * 12.92 : 1.055 * v ** (1 / 2.4) - 0.055);
const toOklab = ([r, g, b]) => {
  const [R, G, B] = [r, g, b].map((v) => srgbToLinear(v / 255));
  const l = Math.cbrt(0.4122214708 * R + 0.5363325363 * G + 0.0514459929 * B);
  const m = Math.cbrt(0.2119034982 * R + 0.6806995451 * G + 0.1073969566 * B);
  const s = Math.cbrt(0.0883024619 * R + 0.2817188376 * G + 0.6299787005 * B);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
};
const fromOklab = ([L, A, B_]) => {
  const l = (L + 0.3963377774 * A + 0.2158037573 * B_) ** 3;
  const m = (L - 0.1055613458 * A - 0.0638541728 * B_) ** 3;
  const s = (L - 0.0894841775 * A - 1.291485548 * B_) ** 3;
  const lin = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
  return lin.map((v) => Math.max(0, Math.min(255, Math.round(linearToSrgb(v) * 255))));
};
const mixOklab = (a, b, t) => {
  const [A, B_] = [toOklab(a), toOklab(b)];
  return fromOklab(A.map((c, i) => c + (B_[i] - c) * t));
};

/**
 * Tangent's own palette — the 18 values it actually states, read out of the
 * screens that use them. Every one of these appears in `ui/src` today; none is
 * chosen here. `warning` is the one judgement call: Tangent has no colour used
 * distinctly as a warning, and amber is already its caution register, so
 * `warning` takes the same value as `primary`. §3.4 permits a theme to set two
 * names equal; flagged to the lead rather than buried.
 */
const T = {
  bg: hex("#090a0c"), // index.css body background, and bg-[#090a0c]
  bgElevated: hex("#0d0f12"),
  surface: hex("#17191d"),
  surfaceHover: hex("#1a1d22"),
  fg: hex("#f2f4f6"),
  fgSecondary: hex("#c5cad1"),
  fgMuted: hex("#8e959f"),
  fgFaint: hex("#606670"),
  border: hex("#3a3e46"),
  borderSubtle: hex("#292c32"),
  divider: hex("#25282e"),
  primary: hex("#f2b84b"),
  brand: hex("#f2b84b"),
  danger: hex("#d96b67"),
  warning: hex("#f2b84b"),
  success: hex("#4faf83"),
  info: hex("#79a7d3"),
  ring: hex("#f2b84b"),
};

const syntax = [0, 0.25, 0.5, 0.75, 1].map((t) => rgb(mixOklab(T.fg, T.fgFaint, t)));

const tokens = {
  bg: rgb(T.bg),
  "bg-elevated": rgb(T.bgElevated),
  surface: rgb(T.surface),
  "surface-hover": rgb(T.surfaceHover),
  "surface-active": r2SurfaceActive(T.surfaceHover, T.fg),
  fg: rgb(T.fg),
  "fg-secondary": rgb(T.fgSecondary),
  "fg-muted": rgb(T.fgMuted),
  "fg-faint": rgb(T.fgFaint),
  border: rgb(T.border),
  "border-subtle": rgb(T.borderSubtle),
  divider: rgb(T.divider),
  primary: rgb(T.primary),
  "primary-hover": r2Hover(T.primary),
  "primary-active": r2Active(T.primary),
  "primary-muted": r1Muted(T.primary),
  "primary-fg": r3Fg(T.primary, T.bg),
  brand: rgb(T.brand),
  "brand-hover": r2Hover(T.brand),
  "brand-active": r2Active(T.brand),
  "brand-muted": r1Muted(T.brand),
  "brand-fg": r3Fg(T.brand, T.bg),
  // index.css already states the selection colour: rgb(242 184 75 / 28%).
  selection: alpha(T.primary, 28),
  "selection-fg": rgb(T.fg),
  ring: rgb(T.ring),
  danger: rgb(T.danger),
  "danger-hover": r2Hover(T.danger),
  "danger-muted": r1Muted(T.danger),
  "danger-fg": r3Fg(T.danger, T.bg),
  warning: rgb(T.warning),
  "warning-muted": r1Muted(T.warning),
  "warning-fg": r3Fg(T.warning, T.bg),
  success: rgb(T.success),
  "success-muted": r1Muted(T.success),
  "success-fg": r3Fg(T.success, T.bg),
  info: rgb(T.info),
  "info-muted": r1Muted(T.info),
  "info-fg": r3Fg(T.info, T.bg),
  // chart-1..5 stay the contract's placeholder magenta. Tangent renders no
  // charts, and the package is explicit that a plausible palette here would be
  // quietly unreviewed. Five identical magentas stay obviously unchosen.
  "chart-1": "rgb(255 0 255)",
  "chart-2": "rgb(255 0 255)",
  "chart-3": "rgb(255 0 255)",
  "chart-4": "rgb(255 0 255)",
  "chart-5": "rgb(255 0 255)",
  "syntax-key": syntax[0],
  "syntax-string": syntax[1],
  "syntax-number": syntax[2],
  "syntax-boolean": syntax[3],
  "syntax-null": syntax[4],
};

/**
 * The contract's two font tokens. tokens.css maps --font-sans/--font-mono onto
 * these with NO fallback, so a theme that declares only the 48 colours leaves
 * every `font-sans` utility and `var(--font-sans)` resolving to nothing — the
 * page silently falls back to the browser default serif. Tangent's own stack is
 * carried over verbatim from the index.css it replaced.
 */
const fonts = {
  "font-sans":
    'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  "font-mono": 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
};

const lines = Object.entries({ ...fonts, ...tokens }).map(([k, v]) => `  --hl-${k}: ${v};`);
process.stdout.write(`/* GENERATED by scripts/gen-tangent-theme.mjs — DO NOT EDIT BY HAND.
 *
 * Tangent's value layer. This is the ONLY layer in this app permitted to name a
 * colour; every component above it names a contract token instead.
 *
 * 18 of these values are Tangent's own, read out of the screens that already
 * used them. The other 30 are derived by the rules documented in
 * @hollis-labs/design-tokens, re-implemented in the generator because the
 * package ships the derived values but not the rules. Regenerate with
 * \`npm run theme:gen\`; \`npm run theme:verify\` checks the rules still
 * reproduce the package's own palettes.
 */
:root {
${lines.join("\n")}
}
`);
