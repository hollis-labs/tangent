#!/usr/bin/env node
/**
 * Envelope Type Generator (Tangent)
 *
 * Reads the registry catalog from `go run ./cmd/tangent-dump-types` and
 * generates:
 *
 *   ui/src/generated/envelope-types.ts     interfaces, the discriminated
 *                                          envelope union, the component-slug
 *                                          map, and the source-digest table
 *   ui/src/generated/renderer-bindings.ts  each kind's manifest-declared
 *                                          renderer binding
 *
 * Usage:
 *   node scripts/generate-envelope-types.mjs
 *   node scripts/generate-envelope-types.mjs --check  # exit 1 if stale
 *
 * There are two sources of truth and the dump tool merges them: the
 * embedded YAML manifest + per-type JSON Schemas in
 * github.com/hollis-labs/go-envelopes for the core catalog, and
 * internal/envelope/extensions for the Tangent-owned kinds. The dump tool
 * builds the registry through the same extensions.RegisterAll the server
 * uses, so every kind Tangent renders is typed here (ADR 0003 §9 S1).
 * This script consumes whatever the dump surfaces and defines nothing of
 * its own.
 *
 * Every emitted file carries a `@definition-source sha256:<hex>` header — the
 * stable hash over the ordered (kind, version, revision, manifest_digest) set
 * of every manifest that contributed to it (ADR 0003 §4.1). --check compares
 * that stamp against a freshly computed one and reports *which kind* drifted,
 * rather than reporting that some byte somewhere differs. It then also compares
 * bytes, because a stamp alone cannot notice a hand-edit to a generated file:
 * the digest question is "is this generated from the current manifests" and the
 * byte question is "is this what the generator produces". Both matter, and they
 * fail with different messages.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '..');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'envelope-types.ts');
const RENDERER_FILE = join(ROOT, 'ui', 'src', 'generated', 'renderer-bindings.ts');

const CHECK_MODE = process.argv.includes('--check');

// --- Catalog load via the Go dump tool ------------------------------------

/**
 * Run `go run ./cmd/tangent-dump-types` from the repo root and parse its
 * JSON stdout. Errors from the Go side propagate with the captured stderr
 * so failures (missing dep, manifest bug) read cleanly in CI logs.
 */
function loadCatalog() {
  const result = spawnSync('go', ['run', './cmd/tangent-dump-types'], {
    cwd: ROOT,
    encoding: 'utf-8',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error) {
    console.error('failed to spawn `go`:', result.error.message);
    process.exit(1);
  }
  if (result.status !== 0) {
    console.error('tangent-dump-types exited non-zero:', result.status);
    if (result.stderr) console.error(result.stderr);
    process.exit(1);
  }
  try {
    return JSON.parse(result.stdout);
  } catch (err) {
    console.error('failed to parse dump-tool JSON:', err.message);
    console.error('stdout was:', result.stdout.slice(0, 500));
    process.exit(1);
  }
}

// --- Naming ---------------------------------------------------------------

/**
 * Convert a wire name to a PascalCase identifier stem:
 * "info-card" → "InfoCard", "tangent.diff-review" → "TangentDiffReview".
 * The dot separator matters — plugin kinds are namespaced, and a bare
 * split on "-" would leave an illegal "." in the emitted identifier.
 */
function toPascal(typeName) {
  return typeName
    .split(/[.\-_]/)
    .filter(Boolean)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join('');
}

/** Convert "info-card" → "InfoCardData" — the per-type data interface name. */
function toDataInterfaceName(typeName) {
  return `${toPascal(typeName)}Data`;
}

/** Convert "info-card" → "InfoCardEnvelope" — the discriminated-union arm name. */
function toEnvelopeInterfaceName(typeName) {
  return `${toPascal(typeName)}Envelope`;
}

// --- JSON-Schema → TS conversion -----------------------------------------
//
// The conversion is deliberately small. go-envelopes' core schemas use a
// narrow subset of JSON Schema (objects + scalars + enums + simple
// arrays + $defs). Mirroring Nanite's generator keeps Tangent and Nanite
// emitting the same shapes.

function jsonTypeToTS(prop) {
  if (!prop) return 'unknown';

  if (Array.isArray(prop.oneOf)) {
    return prop.oneOf.map(jsonTypeToTS).join(' | ');
  }
  if (Array.isArray(prop.anyOf)) {
    return prop.anyOf.map(jsonTypeToTS).join(' | ');
  }
  if (prop.$ref) {
    // Local $ref → bare identifier; we emit $defs as top-level interfaces
    // before consuming refs.
    return prop.$ref.split('/').pop();
  }
  if (Array.isArray(prop.enum)) {
    return prop.enum.map((v) => JSON.stringify(v)).join(' | ');
  }

  switch (prop.type) {
    case 'string':
      return 'string';
    case 'number':
    case 'integer':
      return 'number';
    case 'boolean':
      return 'boolean';
    case 'null':
      return 'null';
    case 'array':
      if (prop.items) {
        return `${jsonTypeToTS(prop.items)}[]`;
      }
      return 'unknown[]';
    case 'object':
      if (prop.properties) {
        return generateInlineObject(prop);
      }
      if (prop.additionalProperties === true) {
        return 'Record<string, unknown>';
      }
      if (prop.additionalProperties && typeof prop.additionalProperties === 'object') {
        return `Record<string, ${jsonTypeToTS(prop.additionalProperties)}>`;
      }
      return 'Record<string, unknown>';
    default:
      return 'unknown';
  }
}

function generateInlineObject(schema) {
  const requiredSet = new Set(schema.required || []);
  const lines = [];
  for (const [key, prop] of Object.entries(schema.properties || {})) {
    const optional = requiredSet.has(key) ? '' : '?';
    lines.push(`${key}${optional}: ${jsonTypeToTS(prop)}`);
  }
  return `{ ${lines.join('; ')} }`;
}

function generateInterface(name, schema, indent = '') {
  const lines = [];
  const requiredSet = new Set(schema.required || []);
  lines.push(`${indent}export interface ${name} {`);
  for (const [key, prop] of Object.entries(schema.properties || {})) {
    const optional = requiredSet.has(key) ? '' : '?';
    if (prop.description) {
      lines.push(`${indent}  /** ${prop.description.replace(/\*\//g, '*\\/')} */`);
    }
    lines.push(`${indent}  ${key}${optional}: ${jsonTypeToTS(prop)};`);
  }
  lines.push(`${indent}}`);
  return lines.join('\n');
}

/**
 * Emit a named declaration for one schema. Object schemas with properties
 * become interfaces; everything else (a `oneOf` union, a bare `$ref` at
 * the document root — both of which the HITL v1 bundle uses) becomes a
 * type alias, because an interface for a non-object schema would silently
 * emit an empty body.
 */
function generateDeclaration(name, schema) {
  if (schema && schema.type === 'object' && schema.properties) {
    return generateInterface(name, schema);
  }
  return `export type ${name} = ${jsonTypeToTS(schema)};`;
}

// --- Code generation ------------------------------------------------------

function generate(catalog) {
  const lines = [];
  const types = catalog.types;

  const coreCount = types.filter((t) => t.source === 'core').length;
  const pluginCount = types.length - coreCount;

  lines.push('// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY');
  lines.push(`// @definition-source ${catalog.definitionSourceDigest}`);
  lines.push(`// Generated from go-envelopes ${catalog.envelopesVersion} — do not edit.`);
  lines.push('// Run `make generate-envelopes` to regenerate.');
  lines.push('//');
  lines.push(`// Coverage: ${types.length} registered kinds — ${coreCount} go-envelopes core,`);
  lines.push(`// ${pluginCount} Tangent-owned (internal/envelope/extensions).`);
  lines.push('//');
  lines.push('// Sources of truth: github.com/hollis-labs/go-envelopes (core catalog)');
  lines.push('// and internal/envelope/extensions (Tangent kinds).');
  lines.push('// Pipeline: cmd/tangent-dump-types -> scripts/generate-envelope-types.mjs');
  lines.push('');

  // Emit $defs first so per-type interfaces can reference them.
  const defsEmitted = new Set();
  for (const t of types) {
    const schema = t.schema;
    if (!schema || !schema.$defs) continue;
    for (const [defName, defSchema] of Object.entries(schema.$defs)) {
      if (defsEmitted.has(defName)) continue;
      defsEmitted.add(defName);
      lines.push(`/** Shared type used by "${t.name}" */`);
      lines.push(generateDeclaration(defName, defSchema));
      lines.push('');
    }
  }

  // Per-type Data interfaces. Types without a schema get an opaque
  // Record fallback so the discriminated union still round-trips them.
  for (const t of types) {
    const dataName = toDataInterfaceName(t.name);
    const desc = t.description || (t.schema && (t.schema.description || t.schema.title)) || '';
    if (desc) {
      lines.push(`/** Envelope data for "${t.name}" — ${desc.replace(/\*\//g, '*\\/').replace(/\n+/g, ' ')} */`);
    } else {
      lines.push(`/** Envelope data for "${t.name}" (no schema registered). */`);
    }
    if (t.hasSchema && t.schema) {
      lines.push(generateDeclaration(dataName, t.schema));
    } else {
      lines.push(`export type ${dataName} = Record<string, unknown>;`);
    }
    lines.push('');
  }

  // Discriminated envelope union — one interface arm per type so the
  // narrowing is via the literal `type` field. This is the workhorse
  // shape the renderer registry will switch on in PR 5.
  lines.push('/** Trace metadata mirrored from go-envelopes Trace. */');
  lines.push('export interface EnvelopeTrace {');
  lines.push('  agentId?: string;');
  lines.push('  sessionId?: string;');
  lines.push('  parentEnvelopeId?: string;');
  lines.push('  createdAt?: string;');
  lines.push('}');
  lines.push('');
  lines.push('/** Presentation hints. Hosts MAY honor and MUST NOT fail on unknown values. */');
  lines.push('export type EnvelopePresentation =');
  lines.push("  | 'inline'");
  lines.push("  | 'modal'");
  lines.push("  | 'drawer'");
  lines.push("  | 'sidecar'");
  lines.push("  | 'fullscreen';");
  lines.push('');
  lines.push('interface EnvelopeBase<TType extends string, TData> {');
  lines.push('  v: number;');
  lines.push('  id: string;');
  lines.push('  type: TType;');
  lines.push('  typeVersion?: string;');
  lines.push('  title?: string;');
  lines.push('  context?: string;');
  lines.push('  presentation?: EnvelopePresentation;');
  lines.push('  data?: TData;');
  lines.push('  trace?: EnvelopeTrace;');
  lines.push('  meta?: Record<string, unknown>;');
  lines.push('}');
  lines.push('');

  for (const t of types) {
    const armName = toEnvelopeInterfaceName(t.name);
    const dataName = toDataInterfaceName(t.name);
    lines.push(`export type ${armName} = EnvelopeBase<"${t.name}", ${dataName}>;`);
  }
  lines.push('');

  lines.push('/** Discriminated union of every envelope type Tangent has registered. */');
  lines.push('export type Envelope =');
  for (let i = 0; i < types.length; i += 1) {
    const armName = toEnvelopeInterfaceName(types[i].name);
    const sep = i === types.length - 1 ? ';' : '';
    lines.push(`  | ${armName}${sep}`);
  }
  lines.push('');

  // String union of envelope type names.
  lines.push('/** String literal union of every registered envelope type name. */');
  lines.push('export type EnvelopeType =');
  for (let i = 0; i < types.length; i += 1) {
    const sep = i === types.length - 1 ? ';' : '';
    lines.push(`  | "${types[i].name}"${sep}`);
  }
  lines.push('');

  // Type → Data map.
  lines.push('/** Maps each envelope type string to its data interface. */');
  lines.push('export interface EnvelopeDataMap {');
  for (const t of types) {
    lines.push(`  "${t.name}": ${toDataInterfaceName(t.name)};`);
  }
  lines.push('}');
  lines.push('');

  // Informational legacy slugs from host-owned manifests. Runtime dispatch
  // uses envelope-registry and renderer-bindings, not this table.
  lines.push('/** Legacy component slugs from host-owned manifests; not runtime renderer bindings. */');
  lines.push('/** Empty string means no legacy slug is declared, including wire-only core kinds. */');
  lines.push('export const EnvelopeKindMap = {');
  for (const t of types) {
    const component = (t.ui && typeof t.ui.component === 'string') ? t.ui.component : '';
    lines.push(`  "${t.name}": ${JSON.stringify(component)},`);
  }
  lines.push('} as const satisfies Record<EnvelopeType, string>;');
  lines.push('');

  // Runtime list — useful for tests and registry probes.
  lines.push('/** All registered envelope type strings, sorted by name. */');
  lines.push('export const ENVELOPE_TYPES: readonly EnvelopeType[] = [');
  for (const t of types) {
    lines.push(`  "${t.name}",`);
  }
  lines.push('] as const;');
  lines.push('');

  // --- Drift detection surface (ADR 0003 §4) ------------------------------
  //
  // The stamp above is a comment, which makes it readable but not usable by
  // running code. These exports are the same values as data, so a client can
  // compare them against what tangent.definition_registry_list reports and
  // refuse to submit against a definition it was not generated for (§4.4),
  // and so a hand-written adapter over a $defs bundle can assert it still
  // matches the bundle it was written against (§4.7).
  lines.push('/** The @definition-source stamp above, as a value. */');
  lines.push(`export const DEFINITION_SOURCE_DIGEST = ${JSON.stringify(catalog.definitionSourceDigest)};`);
  lines.push('');
  lines.push('/** The Tangent release these types were generated against. */');
  lines.push(`export const DEFINITION_HOST_VERSION = ${JSON.stringify(catalog.hostVersion)};`);
  lines.push('');
  lines.push('/** One manifest\'s contribution to the source digest. */');
  lines.push('export interface DefinitionSourceEntry {');
  lines.push('  kind: string;');
  lines.push('  version: string;');
  lines.push('  revision: number;');
  lines.push('  manifestDigest: string;');
  lines.push('  contractDigest: string;');
  lines.push('}');
  lines.push('');
  lines.push('/** Per-kind manifest identity, so drift can name the kind that moved. */');
  lines.push('export const DEFINITION_SOURCE_ENTRIES: readonly DefinitionSourceEntry[] = [');
  for (const t of types) {
    if (!t.definition) continue;
    const d = t.definition;
    lines.push(
      `  { kind: ${JSON.stringify(t.name)}, version: ${JSON.stringify(t.version)}, ` +
      `revision: ${d.revision}, manifestDigest: ${JSON.stringify(d.manifestDigest)}, ` +
      `contractDigest: ${JSON.stringify(d.contractDigest)} },`,
    );
  }
  lines.push('] as const;');
  lines.push('');
  lines.push('/**');
  lines.push(' * Digest of a kind\'s request-schema `$defs` bundle, for the kinds that ship');
  lines.push(' * one. A hand-written adapter over such a bundle stamps itself with this');
  lines.push(' * value and asserts the match in a test — ADR 0003 §4.7\'s accepted floor');
  lines.push(' * when full generation of that adapter is out of scope.');
  lines.push(' */');
  lines.push('export const DEFINITION_DEFS_DIGESTS: Readonly<Record<string, string>> = {');
  for (const t of types) {
    if (!t.definition || !t.definition.defsDigest) continue;
    lines.push(`  ${JSON.stringify(t.name)}: ${JSON.stringify(t.definition.defsDigest)},`);
  }
  lines.push('};');
  lines.push('');
  lines.push('/** Stable `$defs` entry points a kind declares, and what each is for. */');
  lines.push('export const DEFINITION_NAMED_DEFINITIONS: Readonly<');
  lines.push('  Record<string, Readonly<Record<string, string>>>');
  lines.push('> = {');
  for (const t of types) {
    const named = t.definition && t.definition.namedDefinitions;
    if (!named || Object.keys(named).length === 0) continue;
    lines.push(`  ${JSON.stringify(t.name)}: {`);
    for (const name of Object.keys(named).sort()) {
      lines.push(`    ${JSON.stringify(name)}: ${JSON.stringify(named[name])},`);
    }
    lines.push('  },');
  }
  lines.push('};');
  lines.push('');

  return lines.join('\n');
}

/**
 * Emit the renderer bindings each manifest declares.
 *
 * Before ADR 0003 a kind bound to a renderer by three unrelated conventions: a
 * dead `ui.component` slug in the manifest, a string-literal `register()` call
 * in ui/src/main.tsx, and one bespoke route. This table is the manifest's
 * answer, generated, so the registration in main.tsx can be checked against it
 * instead of being the authority (§4.5). It is deliberately data and not a
 * module map: resolving an entry to an imported component is a runtime
 * decision, and CW-20260825-0073 is where trust class starts gating it.
 */
function generateRendererBindings(catalog) {
  const lines = [];
  const managed = catalog.types.filter((t) => t.definition);

  lines.push('// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY');
  lines.push(`// @definition-source ${catalog.definitionSourceDigest}`);
  lines.push('// Run `make generate-envelopes` to regenerate.');
  lines.push('//');
  lines.push(`// The renderer binding declared by each of the ${managed.length} manifests under`);
  lines.push('// internal/envelope/extensions/packages/. ADR 0003 §2.3 makes the manifest the');
  lines.push('// single answer to "what draws this kind"; ui/src/main.tsx is checked against');
  lines.push('// this table rather than being a second source of truth.');
  lines.push('');
  lines.push('/** How a renderer is isolated. */');
  lines.push('export type RendererClass =');
  lines.push("  | 'react-component'");
  lines.push("  | 'declarative'");
  lines.push("  | 'sandboxed-frame'");
  lines.push("  | 'external-surface';");
  lines.push('');
  lines.push('/** Where a renderer runs, and what the browser lets it reach from there. */');
  lines.push('export type RendererIsolation =');
  lines.push("  | 'main-origin'");
  lines.push("  | 'host-primitive'");
  lines.push("  | 'sandboxed-frame'");
  lines.push("  | 'external-surface';");
  lines.push('');
  lines.push('/**');
  lines.push(' * What one isolation is.');
  lines.push(' *');
  lines.push(' * Generated from internal/definition/trust.go: a hand-typed policy table in');
  lines.push(' * the SPA would be a second decider, and the two would disagree the first');
  lines.push(' * time one of them was edited.');
  lines.push(' *');
  lines.push(' * ADR 0009 reduced the five-value trust class to these four positions and');
  lines.push(' * removed the class-based capability ceiling, so this profile no longer');
  lines.push(' * carries one.');
  lines.push(' */');
  lines.push('export interface RendererTrustProfile {');
  lines.push('  isolation: RendererIsolation;');
  lines.push('  /** False for the classes where no publisher-authored code executes at all. */');
  lines.push('  executesPublisherCode: boolean;');
  lines.push("  /** Whether this isolation can reach Tangent's own origin, storage, and session. */");
  lines.push('  ambientHostAuthority: boolean;');
  lines.push('  rendererClasses: readonly string[];');
  lines.push('}');
  lines.push('');
  lines.push('export const RENDERER_TRUST_PROFILES: readonly RendererTrustProfile[] = [');
  for (const profile of catalog.trustProfiles ?? []) {
    lines.push('  {');
    lines.push(`    isolation: ${JSON.stringify(profile.isolation)},`);
    lines.push(`    executesPublisherCode: ${profile.executesPublisherCode ? 'true' : 'false'},`);
    lines.push(`    ambientHostAuthority: ${profile.ambientHostAuthority ? 'true' : 'false'},`);
    lines.push(`    rendererClasses: ${JSON.stringify(profile.rendererClasses)},`);
    lines.push('  },');
  }
  lines.push('] as const;');
  lines.push('');
  lines.push('export interface RendererBinding {');
  lines.push('  /** Wire name of the kind this renderer serves. */');
  lines.push('  kind: string;');
  lines.push('  version: string;');
  lines.push('  /** Stable renderer identity, distinct from kind so one renderer can serve several. */');
  lines.push('  rendererId: string;');
  lines.push('  rendererClass: RendererClass;');
  lines.push('  /** Module specifier and exported symbol, for react-component renderers. */');
  lines.push('  entry: string;');
  lines.push('  /** Where this renderer runs. Declared by the manifest and never substituted: a declaration the host cannot honor quarantines the definition (ADR 0009). */');
  lines.push('  isolation: RendererIsolation;');
  lines.push('  /** Manifest inline payload ceiling, after the host cap. The browser-side bound on untrusted display content. */');
  lines.push('  inlinePayloadLimitBytes: number;');
  lines.push('  /** Declared safe fallback renderer, present only when preserves_meaning is true (ADR 0003 §8 C5). */');
  lines.push('  fallbackRendererId: string;');
  lines.push('  fallbackPreservesMeaning: boolean;');
  lines.push('  fallbackDegradation: string;');
  lines.push('  /** Legacy ui.component slug, empty for kinds that render through their own route. */');
  lines.push('  component: string;');
  lines.push('  packageId: string;');
  lines.push('  /** Materialization state at generation time. */');
  lines.push('  state: string;');
  lines.push('  /** The contract this renderer was generated against. */');
  lines.push('  contractDigest: string;');
  lines.push('}');
  lines.push('');
  lines.push('export const RENDERER_BINDINGS: readonly RendererBinding[] = [');
  for (const t of managed) {
    const d = t.definition;
    const component = (t.ui && typeof t.ui.component === 'string') ? t.ui.component : '';
    lines.push('  {');
    lines.push(`    kind: ${JSON.stringify(t.name)},`);
    lines.push(`    version: ${JSON.stringify(t.version)},`);
    lines.push(`    rendererId: ${JSON.stringify(d.rendererId)},`);
    lines.push(`    rendererClass: ${JSON.stringify(d.rendererClass)},`);
    lines.push(`    entry: ${JSON.stringify(d.rendererEntry)},`);
    lines.push(`    isolation: ${JSON.stringify(d.rendererIsolation)},`);
    lines.push(`    inlinePayloadLimitBytes: ${JSON.stringify(d.inlinePayloadLimitBytes ?? 0)},`);
    lines.push(`    fallbackRendererId: ${JSON.stringify(d.fallbackRendererId ?? '')},`);
    lines.push(`    fallbackPreservesMeaning: ${d.fallbackPreservesMeaning ? 'true' : 'false'},`);
    lines.push(`    fallbackDegradation: ${JSON.stringify(d.fallbackDegradation ?? '')},`);
    lines.push(`    component: ${JSON.stringify(component)},`);
    lines.push(`    packageId: ${JSON.stringify(d.packageId)},`);
    lines.push(`    state: ${JSON.stringify(d.state)},`);
    lines.push(`    contractDigest: ${JSON.stringify(d.contractDigest)},`);
    lines.push('  },');
  }
  lines.push('] as const;');
  lines.push('');
  lines.push('/**');
  lines.push(' * Look one binding up by wire kind.');
  lines.push(' *');
  lines.push(' * A kind with no binding has no manifest-declared renderer, which is not a');
  lines.push(' * lookup miss to paper over: it means nothing classified the renderer, so');
  lines.push(' * nothing may assume it is trusted.');
  lines.push(' */');
  lines.push('export function rendererBindingFor(kind: string): RendererBinding | null {');
  lines.push('  return RENDERER_BINDINGS.find((binding) => binding.kind === kind) ?? null;');
  lines.push('}');
  lines.push('');
  lines.push('/** The profile for one isolation, or null when this build does not implement it. */');
  lines.push('export function trustProfileFor(isolation: string): RendererTrustProfile | null {');
  lines.push('  return RENDERER_TRUST_PROFILES.find((profile) => profile.isolation === isolation) ?? null;');
  lines.push('}');
  lines.push('');
  lines.push('/** Kinds whose manifest says a React component in Tangent\'s own tree draws them. */');
  lines.push('export const REACT_COMPONENT_KINDS: readonly string[] = RENDERER_BINDINGS');
  lines.push("  .filter((binding) => binding.rendererClass === 'react-component')");
  lines.push('  .map((binding) => binding.kind);');
  lines.push('');
  return lines.join('\n');
}

// --- Main -----------------------------------------------------------------

const catalog = loadCatalog();

const artifacts = [
  { path: OUTPUT_FILE, label: 'envelope types', content: generate(catalog) },
  { path: RENDERER_FILE, label: 'renderer bindings', content: generateRendererBindings(catalog) },
];

/** Read the `@definition-source sha256:...` stamp out of a generated file. */
function readStamp(text) {
  const match = /^\/\/ @definition-source (\S+)$/m.exec(text);
  return match ? match[1] : null;
}

/**
 * Report which kinds moved, by diffing the committed per-kind digest table
 * against the fresh one. Naming the kind is the whole point of digest-based
 * staleness detection: a whole-file byte diff can only say "line 412 differs",
 * which is true of a description edit and of a schema rewrite alike.
 */
function reportDriftedKinds(committed) {
  const parseEntries = (text) => {
    const table = /export const DEFINITION_SOURCE_ENTRIES[^[]*\[(.*?)\n\] as const;/s.exec(text);
    const entries = new Map();
    if (!table) return entries;
    const row = /kind: "([^"]+)", version: "([^"]*)", revision: (\d+), manifestDigest: "([^"]*)"/g;
    let found;
    while ((found = row.exec(table[1])) !== null) {
      entries.set(found[1], { version: found[2], revision: Number(found[3]), manifestDigest: found[4] });
    }
    return entries;
  };
  const before = parseEntries(committed);
  const after = new Map(
    catalog.types
      .filter((t) => t.definition)
      .map((t) => [
        t.name,
        { version: t.version, revision: t.definition.revision, manifestDigest: t.definition.manifestDigest },
      ]),
  );

  let reported = 0;
  for (const [kind, fresh] of after) {
    const old = before.get(kind);
    if (!old) {
      console.error(`  + ${kind}@${fresh.version} is newly registered`);
      reported += 1;
    } else if (old.manifestDigest !== fresh.manifestDigest) {
      console.error(
        `  ~ ${kind}: manifest changed (${old.version} rev ${old.revision} -> ${fresh.version} rev ${fresh.revision})`,
      );
      reported += 1;
    }
  }
  for (const kind of before.keys()) {
    if (!after.has(kind)) {
      console.error(`  - ${kind} is no longer registered`);
      reported += 1;
    }
  }
  if (reported === 0) {
    console.error('  (no per-kind difference — the committed digest table itself is stale)');
  }
}

if (CHECK_MODE) {
  let failed = false;
  // The per-kind report is printed once, from the artifact that carries the
  // digest table. Repeating it per artifact would read as several independent
  // drifts when there is only ever one: every artifact is stamped with the same
  // registry-wide digest.
  let reportedDriftedKinds = false;
  for (const artifact of artifacts) {
    if (!existsSync(artifact.path)) {
      console.error(`generated file does not exist: ${artifact.path}`);
      failed = true;
      continue;
    }
    const existing = readFileSync(artifact.path, 'utf-8');
    const committedStamp = readStamp(existing);
    const freshStamp = catalog.definitionSourceDigest;

    // Digest first: it answers "were these generated from the current
    // manifests", and it can say which kind moved.
    if (committedStamp !== freshStamp) {
      console.error(`${artifact.label} are stale — @definition-source does not match the registry.`);
      console.error(`  committed: ${committedStamp ?? '<no stamp>'}`);
      console.error(`  registry:  ${freshStamp}`);
      if (!reportedDriftedKinds && existing.includes('DEFINITION_SOURCE_ENTRIES')) {
        reportDriftedKinds(existing);
        reportedDriftedKinds = true;
      }
      failed = true;
      continue;
    }

    // Bytes second: the stamp cannot notice a hand-edit to a generated file,
    // and a generated file nobody may hand-edit is worth actually enforcing.
    if (existing !== artifact.content) {
      const committedLines = existing.split('\n');
      const freshLines = artifact.content.split('\n');
      const at = freshLines.findIndex((line, i) => committedLines[i] !== line);
      console.error(
        `${artifact.label} match the current manifests but not the current generator output.`,
      );
      if (at !== -1) {
        console.error(`  first difference at line ${at + 1}:`);
        console.error(`    committed: ${committedLines[at] ?? '<end of file>'}`);
        console.error(`    expected:  ${freshLines[at]}`);
      }
      failed = true;
    }
  }
  if (failed) {
    console.error('run: make generate-envelopes');
    process.exit(1);
  }
  console.log(
    `generated artifacts are up to date (${catalog.types.length} kinds, ${catalog.definitionSourceDigest}).`,
  );
  process.exit(0);
}

for (const artifact of artifacts) {
  mkdirSync(dirname(artifact.path), { recursive: true });
  writeFileSync(artifact.path, artifact.content, 'utf-8');
  console.log(`generated ${artifact.label} to ${artifact.path}`);
}
console.log(`definition source digest: ${catalog.definitionSourceDigest}`);
