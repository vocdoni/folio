// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { fromBuilder, humanize, metadataFields, objectFields, prune, toBuilder, type Field } from './schema';
import type { JSONSchema } from './types';

const budget: JSONSchema = {
  type: 'object',
  required: ['items'],
  properties: {
    items: {
      type: 'array',
      items: {
        type: 'object',
        required: ['description', 'amount'],
        properties: { description: { type: 'string' }, quantity: { type: 'string' }, amount: { type: 'string' } },
      },
    },
    totals: {
      type: 'array',
      items: { type: 'object', properties: { label: { type: 'string' }, amount: { type: 'string' }, emphasis: { type: 'boolean' } } },
    },
    notes: { type: 'string', maxLength: 2000 },
  },
};
const milestones: JSONSchema = {
  type: 'array',
  items: { type: 'object', required: ['name'], properties: { name: { type: 'string' }, deliverables: { type: 'array', items: { type: 'string' } } } },
};
const ctx = { components: { budget, milestones, info: { description: 'no data' } } };

describe('form generator', () => {
  it('expands component references', () => {
    const fs = objectFields(
      { type: 'object', properties: { budget: { $ref: 'folio:component/budget' }, plan: { $ref: 'folio:component/milestones' }, info: { $ref: 'folio:component/info' } } },
      ctx,
    )!;
    expect(fs.map((f) => [f.key, f.kind])).toEqual([
      ['budget', 'group'],
      ['plan', 'table'],
    ]);
    const b = fs[0] as Extract<Field, { kind: 'group' }>;
    expect(b.component).toBe('budget');
    expect(b.fields.map((f) => f.kind)).toEqual(['table', 'table', 'text']);
    const items = b.fields[0] as Extract<Field, { kind: 'table' }>;
    expect(items.columns.map((c) => `${c.key}${c.required ? '*' : ''}`)).toEqual(['description*', 'quantity', 'amount*']);
    expect((b.fields[2] as Extract<Field, { kind: 'text' }>).multiline).toBe(true);
    const plan = fs[1] as Extract<Field, { kind: 'table' }>;
    expect(plan.columns[1]!.kind).toBe('list');
  });

  it('falls back to JSON outside the subset', () => {
    const fs = objectFields(
      {
        type: 'object',
        properties: {
          n: { type: 'number' },
          any: { anyOf: [{ type: 'string' }] },
          deep: { type: 'object', properties: { a: { type: 'object', properties: { b: { type: 'object', properties: { c: { type: 'string' } } } } } } },
          ext: { $ref: 'https://example.org/schema' },
        },
      },
      ctx,
    )!;
    expect(fs.map((f) => f.kind)).toEqual(['json', 'json', 'group', 'json']);
    const deep = fs[2] as Extract<Field, { kind: 'group' }>;
    const a = deep.fields[0] as Extract<Field, { kind: 'group' }>;
    expect(a.fields[0]!.kind).toBe('json'); // depth limit
  });

  it('builds document details with dates and the template languages', () => {
    const core: JSONSchema = {
      type: 'object',
      required: ['title', 'date', 'language'],
      properties: { title: { type: 'string' }, date: { type: 'string', pattern: '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' }, language: { enum: ['en', 'es', 'ca'] } },
    };
    const fs = metadataFields(core, { type: 'object', required: ['client'], properties: { client: { type: 'string' }, valid_until: { type: 'string', format: 'date' } } }, ['en', 'es'], ctx);
    expect(fs.map((f) => f.key)).toEqual(['title', 'date', 'language', 'client', 'valid_until']); // required first
    expect(fs.find((f) => f.key === 'language')).toMatchObject({ kind: 'choice', options: ['en', 'es'] });
    expect(fs.find((f) => f.key === 'date')).toMatchObject({ kind: 'text', date: true, required: true });
    expect(fs.find((f) => f.key === 'valid_until')).toMatchObject({ label: 'Valid until', date: true, required: false });
    expect(fs.find((f) => f.key === 'client')!.required).toBe(true);
  });

  it('prunes empty values', () => {
    expect(prune({ a: '', b: ' x ', c: [], d: [{ e: '' }, { e: 'f' }], g: { h: '' }, i: false })).toEqual({ b: ' x ', d: [{ e: 'f' }], i: false });
    expect(prune({})).toBeUndefined();
  });

  it('round-trips builder schemas', () => {
    const s = fromBuilder([
      { name: 'team', label: 'Team', type: 'text', required: true, options: [] },
      { name: 'due', label: '', type: 'date', required: false, options: [] },
      { name: 'tier', label: '', type: 'choice', required: false, options: ['Gold', ' Silver ', ''] },
      { name: 'budget', label: '', type: 'component:budget', required: true, options: [] },
    ])!;
    expect(s.required).toEqual(['team', 'budget']);
    expect(s.properties!.tier!.enum).toEqual(['Gold', 'Silver']);
    expect(toBuilder(s)!.map((f) => f.type)).toEqual(['text', 'date', 'choice', 'component:budget']);
    expect(toBuilder({ type: 'object', properties: { n: { type: 'integer' } } })).toBeNull();
    expect(toBuilder({ type: 'object', properties: {}, allOf: [] })).toBeNull();
    expect(fromBuilder([])).toBeUndefined();
  });

  it('humanizes keys', () => {
    expect(humanize('valid_until')).toBe('Valid until');
    expect(humanize('versionLabel')).toBe('Version label');
  });
});
