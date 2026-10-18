<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { api, query } from '../lib/api';
  import { live } from '../lib/live.svelte';
  import { router } from '../lib/router.svelte';
  import { actionName } from '../lib/format';
  import type { ActivityPage, ActivityRow } from '../lib/types';
  import ActivityList from '../components/ActivityList.svelte';

  let source = $state(router.param('source'));
  let actor = $state(router.param('actor'));
  let action = $state(router.param('action'));
  let outcome = $state(router.param('outcome'));
  let rows = $state<ActivityRow[]>([]);
  let next = $state(0);
  let actors = $state<string[]>([]);
  let actions = $state<string[]>([]);
  let loading = $state(false);
  let error = $state('');

  async function load(more = false) {
    loading = true;
    error = '';
    try {
      const page = await api.get<ActivityPage>('/activity' + query({ source, actor, action, outcome, before: more ? next : 0, limit: 100 }));
      rows = more ? [...rows, ...page.items] : page.items;
      next = page.next_before ?? 0;
      actors = page.actors;
      actions = page.actions;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    router.setParams({ source, actor, action, outcome });
    load();
  });
  // Live rows that match the filters join the top of the list.
  let shown = $derived.by(() => {
    const top = rows[0]?.id ?? 0;
    const incoming = live.rows.filter(
      (r) => r.id > top && (!source || r.source === source) && (!actor || r.actor === actor) && (!action || r.action === action) && (!outcome || r.outcome === outcome),
    );
    return [...incoming, ...rows];
  });
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Activity</h1>
      <p>Every call agents make to Folio and every change made here, newest first. Entries are kept for the retention period set on the server.</p>
    </div>
  </div>
  <form class="filters sheet" onsubmit={(e) => e.preventDefault()} aria-label="Filter activity">
    <div class="field">
      <label for="f-source">Where</label>
      <select id="f-source" bind:value={source}>
        <option value="">Agents and web UI</option><option value="mcp">Agents (MCP)</option><option value="ui">Web UI</option>
      </select>
    </div>
    <div class="field">
      <label for="f-actor">Who</label>
      <select id="f-actor" bind:value={actor}>
        <option value="">Anyone</option>
        {#each actors as a (a)}<option value={a}>{a}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="f-action">What</label>
      <select id="f-action" bind:value={action}>
        <option value="">Any action</option>
        {#each actions as a (a)}<option value={a}>{actionName(a)}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="f-outcome">Outcome</label>
      <select id="f-outcome" bind:value={outcome}>
        <option value="">Any outcome</option><option value="ok">Succeeded</option><option value="error">Failed</option>
      </select>
    </div>
  </form>
  {#if error}<p class="note error">{error}</p>{/if}
  <div class="sheet list" aria-busy={loading}>
    <ActivityList rows={shown} fresh={live.fresh} />
    {#if next}
      <button class="btn more" onclick={() => load(true)} disabled={loading}>Show older entries</button>
    {/if}
  </div>
</div>

<style>
  .filters {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: var(--s-4);
    padding-bottom: var(--s-1);
    margin-bottom: var(--s-5);
  }
  .list {
    padding-top: var(--s-2);
  }
  .more {
    margin-top: var(--s-4);
  }
  @media (max-width: 760px) {
    .filters {
      grid-template-columns: 1fr 1fr;
    }
  }
</style>
