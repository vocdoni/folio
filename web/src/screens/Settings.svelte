<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { session } from '../lib/session.svelte';
  import { clock } from '../lib/format';
  import type { Stats } from '../lib/types';
  import Copy from '../components/Copy.svelte';

  let stats = $state<Stats | null>(null);
  onMount(async () => {
    stats = await api.get<Stats>('/stats');
    await session.refreshAI();
  });
  const mcp = $derived(session.caps?.mcp_url ?? location.origin + '/mcp');
  const claude = $derived(`claude mcp add --transport http folio ${mcp} \\\n  --header "Authorization: Bearer <your-token>"`);
  const codex = $derived(`[mcp_servers.folio]\nurl = "${mcp}"\nbearer_token_env_var = "FOLIO_TOKEN"`);
  const cursor = $derived(JSON.stringify({ mcpServers: { folio: { url: mcp, headers: { Authorization: 'Bearer <your-token>' } } } }, null, 2));
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Settings</h1>
      <p>How this Folio is set up and how to connect agents to it. Settings change through the server’s environment variables.</p>
    </div>
  </div>

  <div class="cols">
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
        <dt>Stored</dt><dd>{stats ? `${stats.documents} documents, ${stats.trashed} in the trash, ${stats.templates} templates, ${stats.brands} brands, ${stats.assets} images` : '…'}</dd>
      </dl>
      <p class="small muted">The assistants send only what you give them (text, an example file, a logo or a website’s colours) to the model, and only when you ask. Their answers are proposals you check before anything is saved.</p>
    </section>

    <section class="sheet" aria-labelledby="connect-h">
      <h2 id="connect-h" class="sheet-title">Connect an agent</h2>
      <p>Agents talk to Folio over MCP with a token from FOLIO_TOKEN or FOLIO_TOKENS. Replace <span class="code">&lt;your-token&gt;</span> with yours.</p>
      <h3>MCP address</h3>
      <Copy text={mcp} label="MCP address" />
      <h3>Claude Code</h3>
      <Copy text={claude} label="Claude Code command" />
      <p class="small muted">Or install the Folio plugin from the repository this Folio was built from: <span class="code">/plugin marketplace add &lt;owner&gt;/&lt;repository&gt;</span>, then <span class="code">/plugin install folio@folio</span>. It asks for the address and the token.</p>
      <h3>Codex</h3>
      <Copy text={codex} label="Codex configuration" />
      <h3>Cursor</h3>
      <Copy text={cursor} label="Cursor configuration" />
    </section>
  </div>
</div>

<style>
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
    gap: var(--s-5);
    align-items: start;
  }
  .sheet + .sheet {
    margin-top: 0;
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
