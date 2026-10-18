// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { match } from './routes';

describe('routes', () => {
  it('matches app paths under /ui', () => {
    expect(match('/ui/')).toEqual({ name: 'overview', params: {} });
    expect(match('/ui')).toEqual({ name: 'overview', params: {} });
    expect(match('/ui/documents/')).toEqual({ name: 'documents', params: {} });
    expect(match('/ui/documents/0199a0f1')).toEqual({ name: 'document', params: { id: '0199a0f1' } });
    expect(match('/ui/templates/new').name).toBe('studio-new');
    expect(match('/ui/templates/proposal')).toEqual({ name: 'studio', params: { id: 'proposal' } });
    expect(match('/ui/brands/new').name).toBe('brand-new');
    expect(match('/ui/nope').name).toBe('not-found');
  });
});
