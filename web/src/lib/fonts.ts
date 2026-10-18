// SPDX-License-Identifier: AGPL-3.0-only
// Loads the bundled document fonts for brand specimens with the FontFace
// API (font-src 'self'; no stylesheet injection).

import type { Capabilities } from './types';

const loaded = new Map<string, Promise<string>>();

/** specimenFamily returns the CSS family name to use for a bundled font. */
export function specimenFamily(caps: Capabilities | null, family: string): Promise<string> {
  const f = caps?.fonts.find((x) => x.family === family);
  if (!f?.regular) return Promise.resolve('inherit');
  let p = loaded.get(family);
  if (!p) {
    const name = `Specimen ${family}`;
    const faces = [new FontFace(name, `url(${f.regular})`, { weight: '400' })];
    if (f.bold) faces.push(new FontFace(name, `url(${f.bold})`, { weight: '700' }));
    p = Promise.all(faces.map((x) => x.load())).then((ok) => {
      ok.forEach((x) => document.fonts.add(x));
      return `'${name}'`;
    });
    loaded.set(family, p);
  }
  return p;
}
