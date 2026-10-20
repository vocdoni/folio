<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { session } from '../lib/session.svelte';
  import { clock } from '../lib/format';
  import type { Connection, Stats } from '../lib/types';
  import Copy from '../components/Copy.svelte';
  import Icon from '../components/Icon.svelte';
  import PiConnect from '../components/PiConnect.svelte';
  import { MASK, claudeInstall, claudePlugin, examplePrompts, skillInstall, snippets } from '../lib/connect';
  import { methodsText } from '../lib/signin';

  let stats = $state<Stats | null>(null);
  let conn = $state<Connection | null>(null);
  let connError = $state('');
  let picked = $state(0);
  let shown = $state(false);
  onMount(async () => {
    stats = await api.get<Stats>('/stats');
    await session.refreshAI();
  });
  onMount(async () => {
    try {
      conn = await api.get<Connection>('/connection');
    } catch (e) {
      connError = e instanceof Error ? e.message : 'Folio could not load the connection details.';
    }
  });
  const mcp = $derived(conn?.mcp_url ?? session.caps?.mcp_url ?? location.origin + '/mcp');
  const base = $derived(mcp.replace(/\/mcp$/, ''));
  const tokens = $derived(conn?.tokens ?? []);
  const current = $derived(tokens[Math.min(picked, Math.max(tokens.length - 1, 0))]);
  // null: this Folio asks for no token. While loading, show a placeholder.
  const realToken = $derived(conn ? (current?.token ?? null) : '<your-token>');
  const real = $derived(snippets({ mcpUrl: mcp, token: realToken }));
  const masked = $derived(snippets({ mcpUrl: mcp, token: realToken !== null && conn ? MASK : realToken }));
  const display = $derived(shown ? real : masked);
  const shownToken = $derived(realToken !== null && conn ? (shown ? realToken : MASK) : realToken);
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Settings</h1>
      <p>How this Folio is set up and how to connect agents to it. Settings change through the server’s environment variables.</p>
    </div>
  </div>

  <div class="cols">
    <div class="stack">
    <section class="sheet" aria-labelledby="about-h">
      <h2 id="about-h" class="sheet-title">This server</h2>
      <dl class="facts">
        <dt>Version</dt><dd class="code">{stats?.version ?? '…'}</dd>
        <dt>Sandbox</dt><dd>{stats?.sandbox_level === 'landlock+rlimits' ? 'Landlock and resource limits' : stats?.sandbox_level === 'rlimits-only' ? 'Resource limits only (no Landlock on this host)' : '…'}</dd>
        <dt>Layout reviewer</dt><dd>{stats?.reviewer ? 'On (page images go to OpenRouter when an agent asks for a review)' : 'Off'}</dd>
        <dt>Assistants</dt>
        <dd>
          {#if session.ai.enabled}
            On, model <span class="code">{session.ai.model}</span>{session.ai.vision ? ', reads images' : ', text only (PDF examples are off)'}.
            <br /><span class="muted small">{session.ai.session_remaining} of {session.ai.session_limit} requests left this hour for you;
              {session.ai.daily_remaining} of {session.ai.daily_limit} left today for everyone{session.ai.daily_resets_at ? ` (resets at ${clock(session.ai.daily_resets_at)})` : ''}.</span>
          {:else}
            Off. Set FOLIO_OPENROUTER_API_KEY on the server to draft templates, fill in details and suggest brands.
          {/if}
        </dd>
        <dt>Sign-in</dt>
        <dd>
          {methodsText(session.info.methods)}
          {#if session.info.method === 'oidc'}
            <br /><span class="muted small">You signed in as <span class="code">{session.info.email}</span> through SSO.</span>
          {:else if session.info.method === 'password'}
            <br /><span class="muted small">You signed in with the password.</span>
          {/if}
        </dd>
        <dt>Stored</dt><dd>{stats ? `${stats.documents} documents, ${stats.trashed} in the trash, ${stats.templates} templates, ${stats.brands} brands, ${stats.assets} images` : '…'}</dd>
      </dl>
      <p class="small muted">The assistants send only what you give them (text, an example file, a logo or a website’s colours) to the model, and only when you ask. Their answers are proposals you check before anything is saved.</p>
    </section>

    <section class="sheet" aria-labelledby="ask-h">
      <h2 id="ask-h" class="sheet-title">Things to ask your agent</h2>
      <p>Once Folio is connected, you can ask for things like this. Folio never changes your wording or your figures.</p>
      <ul class="prompts">
        {#each examplePrompts as p (p.title)}
          <li>
            <div class="ptitle">{p.title}</div>
            <Copy text={p.text} label={`prompt: ${p.title}`} />
          </li>
        {/each}
      </ul>
      <p class="small muted">To build a brand from a website, use Brands in this app: paste the address and Folio suggests colours and a logo for you to check.</p>
    </section>
    </div>

    <div class="stack">
    <section class="sheet" aria-labelledby="connect-h">
      <h2 id="connect-h" class="sheet-title">Connect your agents</h2>
      <p>Agents talk to Folio over MCP. Everything below sets Folio up for your whole account, not just one project.</p>
      <h3>Folio address</h3>
      <Copy text={base} label="Folio address" />
      <p class="small muted">The MCP address, used by the agents further down, is <span class="code">{mcp}</span>.</p>
      {#if connError}
        <p class="note error" role="alert">{connError}</p>
      {:else if conn && tokens.length === 0}
        <p class="note">This Folio does not ask for a token, so nothing below needs one.</p>
      {:else if conn && current}
        <h3>Access token</h3>
        {#if tokens.length > 1}
          <div class="field">
            <label for="token-pick">Token for</label>
            <select id="token-pick" bind:value={picked}>
              {#each tokens as t, i (t.name)}<option value={i}>{t.name}</option>{/each}
            </select>
          </div>
        {/if}
        <div class="tokenrow">
          <Copy text={current.token} display={shown ? current.token : MASK} label={tokens.length > 1 ? `token ${current.name}` : 'token'} />
          <button class="btn small" aria-label="Show token" aria-pressed={shown} onclick={() => (shown = !shown)}>
            <Icon name={shown ? 'hide' : 'show'} />{shown ? 'Hide' : 'Show'}
          </button>
        </div>
        <p class="note warn">Anyone with this token can use Folio as you. Keep it out of shared repositories, and note that pasted commands stay in your shell history.</p>
      {/if}

      <h3>Claude Code</h3>
      <p>Install the Folio plugin. It sets up the MCP connection and the Folio skill together. Claude Code keeps the token with its other credentials.</p>
      <Copy text={claudeInstall(base, realToken)} display={claudeInstall(base, shownToken)} label="Claude Code install command" />
      <p class="small muted">Prefer to type the details yourself? Inside a Claude Code session, run these and paste the Folio address and the access token when asked:</p>
      <Copy text={claudePlugin.inSession} label="Claude Code in-session commands" />

      <h3>Codex</h3>
      <p class="small muted">Add this to <span class="code">~/.codex/config.toml</span>.</p>
      <Copy text={real.codex} display={display.codex} label="Codex configuration" />
      <h3>Cursor</h3>
      <p class="small muted">Save this as <span class="code">~/.cursor/mcp.json</span>.</p>
      <Copy text={real.cursor} display={display.cursor} label="Cursor configuration" />
      <h3>Gemini CLI</h3>
      <p class="small muted">Add this to <span class="code">~/.gemini/settings.json</span>.</p>
      <Copy text={real.gemini} display={display.gemini} label="Gemini CLI configuration" />
      <h3>pi coding agent</h3>
      <PiConnect {base} token={realToken} shown={shownToken} />
    </section>

    <section class="sheet" aria-labelledby="skill-h">
      <h2 id="skill-h" class="sheet-title">Install the skill without the plugin</h2>
      <p>The skill is one file that teaches an agent how to use Folio well. Claude Code gets it from the plugin. For other agents, save it in their skills folder.</p>
      <h3>Codex</h3>
      <Copy text={skillInstall(base, '~/.codex/skills')} label="Codex skill install command" />
      <h3>pi</h3>
      <p class="small muted">The pi setup above already includes this step.</p>
      <h3>Other agents</h3>
      <Copy text={skillInstall(base, '~/.agents/skills')} label="skill install command" />
      <p class="small muted"><span class="code">~/.agents/skills</span> is the shared Agent Skills folder that several agents read. If yours uses its own folder, change the path. You can also <a href="/skills/folio/SKILL.md">read SKILL.md</a> first.</p>
    </section>
    </div>
  </div>
</div>

<style>
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
    gap: var(--s-5);
    align-items: start;
  }
  .stack {
    display: grid;
    gap: var(--s-5);
    min-width: 0;
  }
  .tokenrow {
    display: flex;
    gap: var(--s-2);
    align-items: flex-start;
  }
  .tokenrow :global(.snippet) {
    flex: 1;
  }
  .prompts {
    list-style: none;
    margin: 0 0 var(--s-4);
    padding: 0;
    display: grid;
    gap: var(--s-3);
  }
  .ptitle {
    font-weight: 600;
    color: var(--ink-2);
    font-size: var(--fs-0);
    margin-bottom: var(--s-1);
  }
  .facts {
    display: grid;
    grid-template-columns: 9rem 1fr;
    gap: var(--s-2) var(--s-4);
    margin: 0 0 var(--s-4);
  }
  dt {
    font-weight: 600;
    color: var(--ink-2);
  }
  dd {
    margin: 0;
  }
  h3 {
    font-size: var(--fs-1);
    margin: var(--s-4) 0 var(--s-2);
  }
  @media (max-width: 960px) {
    .cols {
      grid-template-columns: 1fr;
    }
    .facts {
      grid-template-columns: 1fr;
    }
  }
</style>
