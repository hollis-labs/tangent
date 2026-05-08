#!/usr/bin/env node
/**
 * Envelope Type Generator (Tangent)
 *
 * Reads the registry catalog from `go run ./cmd/tangent-dump-types` and
 * generates TypeScript interfaces + a discriminated envelope union +
 * a component-slug map for the renderer registry (PR 5).
 *
 * Output: ui/src/generated/envelope-types.ts
 *
 * Usage:
 *   node scripts/generate-envelope-types.mjs
 *   node scripts/generate-envelope-types.mjs --check  # diff against committed file; exit 1 if stale
 *
 * Source of truth is the embedded YAML manifest + per-type JSON Schemas
 * in github.com/hollis-labs/go-envelopes. Tangent does NOT define its
 * own envelope schemas; this script consumes whatever the dump tool
 * surfaces. Tangent-specific types, when introduced, will register
 * through the plugin extension API and flow through the same dump.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '..');
const OUTPUT_FILE = join(ROOT, 'ui', 'src', 'generated', 'envelope-types.ts');

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

/** Convert "info-card" → "InfoCard". */
function toPascal(typeName) {
  return typeName
    .split('-')
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

// --- Code generation ------------------------------------------------------

function generate(catalog) {
  const lines = [];
  const types = catalog.types;

  lines.push('// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY');
  lines.push(`// Generated from go-envelopes ${catalog.envelopesVersion} — do not edit.`);
  lines.push('// Run `make generate-envelopes` to regenerate.');
  lines.push('//');
  lines.push('// Source of truth: github.com/hollis-labs/go-envelopes');
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
      lines.push(generateInterface(defName, defSchema));
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
      lines.push(generateInterface(dataName, t.schema));
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

  lines.push('/** Discriminated union of every envelope type known to go-envelopes core. */');
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

  // EnvelopeKindMap: type → component slug. Source = ui.component when set.
  // Used by the renderer registry in PR 5 to look up React components.
  lines.push('/** Maps envelope type -> component import slug from the YAML manifest. */');
  lines.push('/** Empty string means the type has no frontend component yet. */');
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

  return lines.join('\n');
}

// --- Main -----------------------------------------------------------------

const catalog = loadCatalog();
const output = generate(catalog);

if (CHECK_MODE) {
  if (!existsSync(OUTPUT_FILE)) {
    console.error('generated file does not exist:', OUTPUT_FILE);
    console.error('run: make generate-envelopes');
    process.exit(1);
  }
  const existing = readFileSync(OUTPUT_FILE, 'utf-8');
  if (existing !== output) {
    console.error('generated envelope types are stale.');
    console.error('run: make generate-envelopes');
    process.exit(1);
  }
  console.log('envelope types are up to date.');
  process.exit(0);
}

mkdirSync(dirname(OUTPUT_FILE), { recursive: true });
writeFileSync(OUTPUT_FILE, output, 'utf-8');
console.log(`generated ${catalog.types.length} envelope type definitions to ${OUTPUT_FILE}`);
