// SPDX-License-Identifier: AGPL-3.0-only
// Naming images: the suggested name for an upload and the limits the server
// enforces (name 1-200 characters, description at most 500).

export const NAME_MAX = 200;
export const DESCRIPTION_MAX = 500;

/** suggestedName turns "northwind-logo.final.png" into "northwind-logo.final". */
export function suggestedName(fileName: string): string {
  const base = fileName.split(/[\\/]/).pop() ?? fileName;
  const dot = base.lastIndexOf('.');
  const stem = dot > 0 ? base.slice(0, dot) : base;
  return [...(stem.trim() || base.trim())].slice(0, NAME_MAX).join('');
}

export interface NamingRow {
  name: string;
  description: string;
}

/** nameProblem returns the message for a name field, or "" when it is fine. */
export function nameProblem(name: string): string {
  const n = [...name.trim()].length;
  if (n === 0) return 'Give the image a name.';
  if (n > NAME_MAX) return `Use at most ${NAME_MAX} characters.`;
  return '';
}

/** descriptionProblem returns the message for a description field, or "". */
export function descriptionProblem(description: string): string {
  return [...description.trim()].length > DESCRIPTION_MAX ? `Use at most ${DESCRIPTION_MAX} characters.` : '';
}

/** rowsValid reports whether every row can be sent. */
export function rowsValid(rows: NamingRow[]): boolean {
  return rows.length > 0 && rows.every((r) => !nameProblem(r.name) && !descriptionProblem(r.description));
}
