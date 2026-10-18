// SPDX-License-Identifier: AGPL-3.0-only
// The live activity feed (server-sent events). One connection for the app;
// screens read `rows` (newest first) and `status`.

import type { ActivityRow } from './types';

const KEEP = 200;

class LiveFeed {
  rows = $state<ActivityRow[]>([]);
  status = $state<'off' | 'connecting' | 'live' | 'retrying'>('off');
  /** ids of rows that arrived while the page was open (they slide in once). */
  fresh = $state<Set<number>>(new Set());
  private es: EventSource | null = null;
  private retry: ReturnType<typeof setTimeout> | null = null;

  start() {
    if (this.es) return;
    this.status = 'connecting';
    const es = new EventSource('/ui/api/activity/stream');
    this.es = es;
    es.onopen = () => (this.status = 'live');
    es.addEventListener('activity', (e) => {
      const row = JSON.parse((e as MessageEvent<string>).data) as ActivityRow;
      if (this.rows.some((r) => r.id === row.id)) return;
      this.rows = [row, ...this.rows].slice(0, KEEP);
      this.fresh = new Set([...this.fresh, row.id]);
    });
    es.addEventListener('bye', () => this.reconnectLater(5000));
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) this.reconnectLater(5000);
      else this.status = 'retrying';
    };
  }

  /** seed adds rows loaded through the list API (older history). */
  seed(rows: ActivityRow[]) {
    const known = new Set(this.rows.map((r) => r.id));
    this.rows = [...this.rows, ...rows.filter((r) => !known.has(r.id))].sort((a, b) => b.id - a.id).slice(0, KEEP);
  }

  stop() {
    this.es?.close();
    this.es = null;
    if (this.retry) clearTimeout(this.retry);
    this.retry = null;
    this.status = 'off';
  }

  private reconnectLater(ms: number) {
    this.es?.close();
    this.es = null;
    this.status = 'retrying';
    if (this.retry) clearTimeout(this.retry);
    this.retry = setTimeout(() => {
      this.retry = null;
      this.start();
    }, ms);
  }
}

export const live = new LiveFeed();
