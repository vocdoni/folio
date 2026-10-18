// SPDX-License-Identifier: AGPL-3.0-only
// Route table of the app (pure: unit-tested without a DOM).

export const ROOT = '/ui';

export interface Route {
  name: string;
  params: Record<string, string>;
}

const patterns: [string, RegExp][] = [
  ['overview', /^\/$/],
  ['activity', /^\/activity$/],
  ['documents', /^\/documents$/],
  ['document', /^\/documents\/([^/]+)$/],
  ['create', /^\/create$/],
  ['templates', /^\/templates$/],
  ['studio-new', /^\/templates\/new$/],
  ['studio', /^\/templates\/([^/]+)$/],
  ['brands', /^\/brands$/],
  ['brand-new', /^\/brands\/new$/],
  ['brand', /^\/brands\/([^/]+)$/],
  ['assets', /^\/assets$/],
  ['settings', /^\/settings$/],
];

/** match maps a path below /ui to a route. */
export function match(path: string): Route {
  let p = path.startsWith(ROOT) ? path.slice(ROOT.length) : path;
  if (p === '') p = '/';
  if (p.length > 1 && p.endsWith('/')) p = p.slice(0, -1);
  for (const [name, re] of patterns) {
    const m = re.exec(p);
    if (m) return { name, params: m[1] !== undefined ? { id: decodeURIComponent(m[1]) } : {} };
  }
  return { name: 'not-found', params: {} };
}

