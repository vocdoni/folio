// SPDX-License-Identifier: AGPL-3.0-only
// Counts long operations (renders, previews, assistant jobs) so the
// masthead's colour bar can show that Folio is working.

class Work {
  n = $state(0);
  busy = $derived(this.n > 0);

  async run<T>(p: Promise<T>): Promise<T> {
    this.n++;
    try {
      return await p;
    } finally {
      this.n--;
    }
  }
}

export const work = new Work();
