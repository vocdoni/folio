// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { query } from './api';
import { actionName, ago, bytes, count, isInline, percent } from './format';

describe('format', () => {
  it('formats sizes, rates and counts', () => {
    expect(bytes(512)).toBe('512 B');
    expect(bytes(1536)).toBe('1.5 KB');
    expect(bytes(48 * 1024 * 1024)).toBe('48 MB');
    expect(`${bytes(0)} of ${bytes(1048576)}`).toBe('0 B of 1.0 MB');
    expect(percent(0)).toBe('0%');
    expect(percent(0.004)).toBe('0.4%');
    expect(percent(0.25)).toBe('25%');
    expect(count(1, 'document')).toBe('1 document');
    expect(count(3, 'document')).toBe('3 documents');
  });
  it('names actions in plain language', () => {
    expect(actionName('render_document')).toBe('Rendered a document');
    expect(actionName('ui.login')).toBe('Signed in');
    expect(actionName('something_new')).toBe('something_new');
  });
  it('names inline templates', () => {
    expect(isInline('inline-0123456789abcdef')).toBe(true);
    expect(isInline('inline')).toBe(false);
    expect(actionName('save_inline_template')).toBe('Saved an inline template as a template');
  });
  it('says how long ago', () => {
    const now = Date.parse('2026-10-18T12:00:00Z');
    expect(ago('2026-10-18T11:59:50Z', now)).toBe('just now');
    expect(ago('2026-10-18T11:55:00Z', now)).toBe('5 min ago');
    expect(ago('2026-10-18T09:00:00Z', now)).toBe('3 h ago');
  });
  it('builds query strings from set values only', () => {
    expect(query({ a: 'x y', b: '', c: undefined, d: 3, e: false, f: true })).toBe('?a=x+y&d=3&f=true');
    expect(query({})).toBe('');
  });
});
