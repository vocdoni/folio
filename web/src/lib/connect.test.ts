// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { MASK, claudeInstall, claudePlugin, examplePrompts, piSetup, skillInstall, snippets } from './connect';

const url = 'https://folio.example.com/mcp';
const base = 'https://folio.example.com';
const fake = 'fake-token-0123456789abcdef0123456789';

describe('Claude Code', () => {
  it('installs the plugin for the user with one command that carries the address and token', () => {
    const c = claudeInstall(base, fake);
    expect(c).toContain('claude plugin install folio --marketplace vocdoni/folio --scope user');
    expect(c).toContain(`--config folio_url=${base}`);
    expect(c).not.toContain(`${base}/mcp`);
    expect(c).toContain(`--config folio_token='${fake}'`);
  });

  it('masks the token for display and omits it when none is needed', () => {
    expect(claudeInstall(base, MASK)).toContain(`folio_token='${MASK}'`);
    expect(claudeInstall(base, null)).not.toContain('folio_token');
  });

  it('keeps the in-session form', () => {
    expect(claudePlugin.inSession).toContain('/plugin marketplace add vocdoni/folio');
    expect(claudePlugin.inSession).toContain('/plugin install folio@folio');
  });

  it('never shows a manual claude mcp add', () => {
    const all = JSON.stringify([claudePlugin, claudeInstall(base, fake), snippets({ mcpUrl: url, token: fake })]);
    expect(all).not.toContain('claude mcp add');
  });
});

describe('snippets', () => {
  it('puts the token in the Codex, Cursor and Gemini files', () => {
    const s = snippets({ mcpUrl: url, token: fake });
    for (const text of [s.codex, s.cursor, s.gemini]) {
      expect(text).toContain(fake);
      expect(text).toContain(url);
    }
    expect(JSON.parse(s.cursor).mcpServers.folio.headers.Authorization).toBe(`Bearer ${fake}`);
    expect(JSON.parse(s.gemini).mcpServers.folio.httpUrl).toBe(url);
    expect(s.codex).toContain('[mcp_servers.folio]');
  });

  it('masks the token for display', () => {
    const shown = snippets({ mcpUrl: url, token: MASK });
    for (const text of Object.values(shown)) {
      expect(text).not.toContain(fake);
      expect(text).toContain(MASK);
    }
  });

  it('omits the header when no token is needed', () => {
    const s = snippets({ mcpUrl: url, token: null });
    expect(s.cursor).not.toContain('Authorization');
    expect(s.codex).not.toContain('http_headers');
    expect(s.gemini).not.toContain('Authorization');
  });
});

describe('skillInstall', () => {
  it('saves SKILL.md into the agent skills folder', () => {
    expect(skillInstall(base, '~/.codex/skills')).toBe(
      `mkdir -p ~/.codex/skills/folio && curl -fsSL ${base}/skills/folio/SKILL.md -o ~/.codex/skills/folio/SKILL.md`,
    );
  });
});

describe('piSetup', () => {
  it('registers Folio at user level through an environment variable', () => {
    const { commands, json } = piSetup(base, fake);
    expect(commands).toContain(`export FOLIO_TOKEN='${fake}'`);
    expect(commands).toContain(`pi mcp add folio --url ${base}/mcp --bearer-token-env-var FOLIO_TOKEN`);
    expect(commands).toContain('pi mcp list');
    expect(commands).toContain(
      `mkdir -p ~/.pi/agent/skills/folio && curl -fsSL ${base}/skills/folio/SKILL.md -o ~/.pi/agent/skills/folio/SKILL.md`,
    );
    expect(JSON.parse(json)).toEqual({ mcpServers: { folio: { url: `${base}/mcp`, headers: { Authorization: 'Bearer ${FOLIO_TOKEN}' } } } });
    expect(json).not.toContain(fake);
  });

  it('skips the token steps when none is needed', () => {
    const { commands, json } = piSetup(base, null);
    expect(commands).not.toContain('FOLIO_TOKEN');
    expect(json).not.toContain('Authorization');
  });
});

describe('examplePrompts', () => {
  it('offers eight to ten distinct prompts', () => {
    expect(examplePrompts.length).toBeGreaterThanOrEqual(8);
    expect(examplePrompts.length).toBeLessThanOrEqual(10);
    expect(new Set(examplePrompts.map((p) => p.text)).size).toBe(examplePrompts.length);
    for (const p of examplePrompts) expect(p.text.length).toBeGreaterThan(30);
  });
});
