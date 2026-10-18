// SPDX-License-Identifier: AGPL-3.0-only
// The form generator's model: which JSON Schemas become which form fields.
// Supported subset: objects (properties, required, depth ≤ 3); string
// (format date or a date pattern → date input, enum → select, maxLength),
// boolean, array of strings, array of objects (a table whose columns are
// themselves leaves) and $ref "folio:component/<name>" (expanded from the
// component's own schema). Anything else is edited as raw JSON. Figures are
// strings; number and integer types fall back to JSON.

import type { JSONSchema } from './types';

interface Base {
  key: string;
  label: string;
  help?: string;
  required: boolean;
}

export type Field =
  | (Base & { kind: 'text'; multiline: boolean; maxLength?: number; date: boolean })
  | (Base & { kind: 'choice'; options: string[] })
  | (Base & { kind: 'bool' })
  | (Base & { kind: 'list' })
  | (Base & { kind: 'table'; columns: Field[]; component?: string })
  | (Base & { kind: 'group'; fields: Field[]; component?: string })
  | (Base & { kind: 'json'; reason: string });

export interface Context {
  /** component schemas by name (from /capabilities). */
  components: Record<string, JSONSchema>;
}

const MAX_DEPTH = 3;
const DATE_RE = /^\^?\[0-9\]\{4\}-\[0-9\]\{2\}-\[0-9\]\{2\}\$?$|^\^?\\d\{4\}-\\d\{2\}-\\d\{2\}\$?$/;
const LONG = new Set(['notes', 'description', 'acceptance', 'summary', 'hint']);

/** humanize turns "valid_until" into "Valid until". */
export function humanize(key: string): string {
  const s = key.replace(/[_-]+/g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2').trim().toLowerCase();
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function typeOf(s: JSONSchema): string | undefined {
  if (Array.isArray(s.type)) return s.type.length === 1 ? s.type[0] : undefined;
  if (s.type) return s.type;
  if (s.properties) return 'object';
  if (s.enum) return 'string';
  return undefined;
}

export function isDatePattern(p: string | undefined): boolean {
  return !!p && DATE_RE.test(p);
}

function componentName(ref: string | undefined): string | undefined {
  const m = /^folio:component\/([a-z]+)$/.exec(ref ?? '');
  return m?.[1];
}

/** field maps one property schema to a form field. */
export function field(key: string, s: JSONSchema, required: boolean, ctx: Context, depth: number): Field | null {
  const base: Base = { key, label: s.title ?? humanize(key), help: s.description, required };
  const comp = componentName(s.$ref);
  if (s.$ref) {
    if (!comp) return { ...base, kind: 'json', reason: 'references another schema' };
    const cs = ctx.components[comp];
    if (!cs) return { ...base, kind: 'json', reason: `unknown component ${comp}` };
    if (!typeOf(cs)) return null; // info: renders from metadata, no data
    const f = field(key, { ...cs, title: s.title ?? humanize(comp), description: s.description }, required, ctx, depth);
    if (f && (f.kind === 'table' || f.kind === 'group')) f.component = comp;
    return f;
  }
  if (s.anyOf || s.oneOf || s.allOf || s.not || s.if) return { ...base, kind: 'json', reason: 'uses combined schemas' };
  switch (typeOf(s)) {
    case 'string':
      if (Array.isArray(s.enum) && s.enum.every((v) => typeof v === 'string')) {
        return { ...base, kind: 'choice', options: s.enum as string[] };
      }
      return {
        ...base,
        kind: 'text',
        date: s.format === 'date' || isDatePattern(s.pattern),
        maxLength: s.maxLength,
        multiline: LONG.has(key) || (s.maxLength ?? 0) > 300,
      };
    case 'boolean':
      return { ...base, kind: 'bool' };
    case 'array': {
      const it = s.items;
      if (!it) return { ...base, kind: 'json', reason: 'an array without item schema' };
      const t = typeOf(it);
      if (t === 'string' && !it.enum) return { ...base, kind: 'list' };
      if (t === 'object' && depth < MAX_DEPTH) {
        const cols = objectFields(it, ctx, depth + 1);
        if (cols && cols.every((c) => c.kind === 'text' || c.kind === 'choice' || c.kind === 'bool' || c.kind === 'list')) {
          return { ...base, kind: 'table', columns: cols };
        }
      }
      return { ...base, kind: 'json', reason: 'a list of complex items' };
    }
    case 'object': {
      if (depth >= MAX_DEPTH) return { ...base, kind: 'json', reason: 'nested more than three levels' };
      const fs = objectFields(s, ctx, depth + 1);
      return fs ? { ...base, kind: 'group', fields: fs } : { ...base, kind: 'json', reason: 'an object without properties' };
    }
    case 'number':
    case 'integer':
      return { ...base, kind: 'json', reason: 'figures are text in Folio; edit this number as JSON' };
  }
  return { ...base, kind: 'json', reason: 'a type the form does not support' };
}

/** objectFields lists the fields of an object schema (null when it has no properties). */
export function objectFields(s: JSONSchema | undefined, ctx: Context, depth = 1): Field[] | null {
  if (!s || !s.properties) return null;
  const req = new Set(s.required ?? []);
  const out: Field[] = [];
  for (const [k, ps] of Object.entries(s.properties)) {
    const f = field(k, ps, req.has(k), ctx, depth);
    if (f) out.push(f);
  }
  return out;
}

/** mergeObjects combines object schemas (core metadata allOf template metadata). */
export function mergeObjects(...schemas: (JSONSchema | undefined)[]): JSONSchema {
  const out: JSONSchema = { type: 'object', properties: {}, required: [] };
  for (const s of schemas) {
    if (!s) continue;
    Object.assign(out.properties!, s.properties ?? {});
    for (const r of s.required ?? []) if (!out.required!.includes(r)) out.required!.push(r);
  }
  return out;
}

/** metadataFields is the document-details form: core fields first, the
 *  language limited to the template's languages. */
export function metadataFields(core: JSONSchema, template: JSONSchema | undefined, languages: string[], ctx: Context): Field[] {
  const merged = mergeObjects(core, template);
  if (merged.properties?.language) merged.properties.language = { ...merged.properties.language, enum: languages };
  const fs = objectFields(merged, ctx) ?? [];
  // Required details first (stable), then the optional ones.
  return [...fs.filter((f) => f.required), ...fs.filter((f) => !f.required)];
}

type Obj = Record<string, unknown>;

/** prune drops empty strings, empty lists, empty objects and table rows
 *  with no values, so optional fields are simply absent. */
export function prune(v: unknown): unknown {
  if (typeof v === 'string') return v.trim() === '' ? undefined : v;
  if (Array.isArray(v)) {
    const a = v.map(prune).filter((x) => x !== undefined);
    return a.length ? a : undefined;
  }
  if (v && typeof v === 'object') {
    const o: Obj = {};
    for (const [k, x] of Object.entries(v as Obj)) {
      const p = prune(x);
      if (p !== undefined) o[k] = p;
    }
    return Object.keys(o).length ? o : undefined;
  }
  return v === null ? undefined : v;
}

/** pointer builds a JSON pointer from path segments. */
export function pointer(...segs: (string | number)[]): string {
  return segs.map((s) => '/' + String(s).replaceAll('~', '~0').replaceAll('/', '~1')).join('');
}

// ---- schema builder (studio): the subset as a flat list of fields ----

export type BuilderType = 'text' | 'long-text' | 'date' | 'choice' | 'yes-no' | 'list' | `component:${string}`;

export interface BuilderField {
  name: string;
  label: string;
  type: BuilderType;
  required: boolean;
  options: string[];
}

/** toBuilder reads a schema the builder can edit, or null when it uses
 *  anything outside the builder's subset (then it is edited as JSON). */
export function toBuilder(s: JSONSchema | undefined): BuilderField[] | null {
  if (!s) return [];
  if (typeOf(s) !== 'object') return null;
  const known = new Set(['type', 'properties', 'required', 'additionalProperties', 'title', 'description']);
  if (Object.keys(s).some((k) => !known.has(k))) return null;
  const req = new Set(s.required ?? []);
  const out: BuilderField[] = [];
  for (const [name, p] of Object.entries(s.properties ?? {})) {
    const f: BuilderField = { name, label: p.title ?? '', type: 'text', required: req.has(name), options: [] };
    const comp = componentName(p.$ref);
    const t = typeOf(p);
    if (comp) f.type = `component:${comp}`;
    else if (t === 'string' && Array.isArray(p.enum)) {
      f.type = 'choice';
      f.options = p.enum.map(String);
    } else if (t === 'string' && (p.format === 'date' || isDatePattern(p.pattern))) f.type = 'date';
    else if (t === 'string') f.type = (p.maxLength ?? 0) > 300 ? 'long-text' : 'text';
    else if (t === 'boolean') f.type = 'yes-no';
    else if (t === 'array' && p.items && typeOf(p.items) === 'string') f.type = 'list';
    else return null;
    out.push(f);
  }
  return out;
}

/** fromBuilder writes the builder's fields back as a JSON Schema. */
export function fromBuilder(fields: BuilderField[]): JSONSchema | undefined {
  if (fields.length === 0) return undefined;
  const props: Record<string, JSONSchema> = {};
  const required: string[] = [];
  for (const f of fields) {
    const name = f.name.trim();
    if (!name) continue;
    let p: JSONSchema;
    switch (f.type) {
      case 'text':
        p = { type: 'string', maxLength: 300 };
        break;
      case 'long-text':
        p = { type: 'string', maxLength: 4000 };
        break;
      case 'date':
        p = { type: 'string', pattern: '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' };
        break;
      case 'choice':
        p = { type: 'string', enum: f.options.map((o) => o.trim()).filter(Boolean) };
        break;
      case 'yes-no':
        p = { type: 'boolean' };
        break;
      case 'list':
        p = { type: 'array', items: { type: 'string' } };
        break;
      default:
        p = { $ref: `folio:component/${f.type.slice('component:'.length)}` };
    }
    if (f.label.trim()) p.title = f.label.trim();
    props[name] = p;
    if (f.required) required.push(name);
  }
  const out: JSONSchema = { type: 'object', properties: props };
  if (required.length) out.required = required;
  return out;
}
