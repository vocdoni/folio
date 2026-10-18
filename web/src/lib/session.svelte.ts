// SPDX-License-Identifier: AGPL-3.0-only
// Who is signed in, plus what the whole app needs once: capabilities and
// the assistants' status.

import { api, onUnauthorized } from './api';
import type { AIStatus, Capabilities, Session } from './types';

class SessionStore {
  state = $state<'checking' | 'signed-out' | 'signed-in'>('checking');
  info = $state<Session>({ authenticated: false });
  caps = $state<Capabilities | null>(null);
  ai = $state<AIStatus>({ enabled: false, vision: false });

  constructor() {
    onUnauthorized(() => {
      if (this.state === 'signed-in') this.state = 'signed-out';
    });
  }

  async check() {
    try {
      this.info = await api.get<Session>('/session');
      this.state = this.info.authenticated ? 'signed-in' : 'signed-out';
      if (this.info.authenticated) await this.load();
    } catch {
      this.state = 'signed-out';
    }
  }

  async signIn(password: string) {
    this.info = await api.post<Session>('/login', { password }, { quiet401: true });
    this.state = 'signed-in';
    await this.load();
  }

  async signOut() {
    try {
      await api.post('/logout');
    } finally {
      this.state = 'signed-out';
      this.info = { authenticated: false };
    }
  }

  async load() {
    const [caps, ai] = await Promise.all([api.get<Capabilities>('/capabilities'), api.get<AIStatus>('/ai/status')]);
    this.caps = caps;
    this.ai = ai;
  }

  async refreshAI() {
    this.ai = await api.get<AIStatus>('/ai/status');
  }
}

export const session = new SessionStore();
