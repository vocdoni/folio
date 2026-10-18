// SPDX-License-Identifier: AGPL-3.0-only
// Short confirmations ("Published.") announced politely to screen readers.

export interface Toast {
  id: number;
  text: string;
  kind: 'ok' | 'error';
}

class Toasts {
  items = $state<Toast[]>([]);
  private n = 0;

  show(text: string, kind: Toast['kind'] = 'ok') {
    const t = { id: ++this.n, text, kind };
    this.items = [...this.items, t];
    setTimeout(() => this.dismiss(t.id), kind === 'error' ? 8000 : 4000);
  }

  dismiss(id: number) {
    this.items = this.items.filter((t) => t.id !== id);
  }
}

export const toasts = new Toasts();
