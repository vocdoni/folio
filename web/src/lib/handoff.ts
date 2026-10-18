// SPDX-License-Identifier: AGPL-3.0-only
// Hands an assistant proposal from one screen to the editor it opens in
// (in memory only: proposals are never saved until the user saves them).

import type { BrandSuggestion, DraftResult } from './types';

export const handoff: { draft: DraftResult | null; brand: BrandSuggestion | null; brandJob: string } = {
  draft: null,
  brand: null,
  brandJob: '',
};
