// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { descriptionProblem, nameProblem, rowsValid, suggestedName } from './assetNames';

describe('asset names', () => {
  it('prefills the name from the file name without its extension', () => {
    expect(suggestedName('northwind-logo.png')).toBe('northwind-logo');
    expect(suggestedName('team photo.final.JPEG')).toBe('team photo.final');
    expect(suggestedName('logo')).toBe('logo');
    expect(suggestedName('.hidden')).toBe('.hidden');
    expect(suggestedName('C:\\pics\\mark.svg')).toBe('mark');
    expect(suggestedName('x'.repeat(300) + '.png')).toHaveLength(200);
  });

  it('requires a name of 1 to 200 characters', () => {
    expect(nameProblem('  ')).not.toBe('');
    expect(nameProblem('Logo')).toBe('');
    expect(nameProblem('é'.repeat(200))).toBe('');
    expect(nameProblem('a'.repeat(201))).not.toBe('');
  });

  it('allows descriptions up to 500 characters', () => {
    expect(descriptionProblem('')).toBe('');
    expect(descriptionProblem('d'.repeat(500))).toBe('');
    expect(descriptionProblem('d'.repeat(501))).not.toBe('');
  });

  it('is valid only when every row is', () => {
    expect(rowsValid([])).toBe(false);
    expect(rowsValid([{ name: 'a', description: '' }, { name: 'b', description: 'x' }])).toBe(true);
    expect(rowsValid([{ name: 'a', description: '' }, { name: '', description: '' }])).toBe(false);
  });
});
