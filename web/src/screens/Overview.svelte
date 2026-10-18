<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { live } from '../lib/live.svelte';
  import { href } from '../lib/router.svelte';
  import { ago, bytes, count, percent } from '../lib/format';
  import type { ActivityPage, DocumentSummary, Stats } from '../lib/types';
  import ActivityList from '../components/ActivityList.svelte';
  import Proof from '../components/Proof.svelte';

  let stats = $state<Stats | null>(null);
  let docs = $state<DocumentSummary[]>([]);
  let error = $state('');

  async function load() {
    try {
      const [s, d, a] = await Promise.all([
        api.get<Stats>('/stats'),
        api.get<{ items: DocumentSummary[] }>('/documents?limit=6'),
        api.get<ActivityPage>('/activity?limit=40'),
      ]);
      stats = s;
      docs = d.items;
      live.seed(a.items);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(load);
  // Refresh the figures when a render or a deletion arrives on the live feed.
  let lastSeen = 0;
  $effect(() => {
    const top = live.rows[0];
    if (!top || top.id === lastSeen) return;
    const first = lastSeen === 0;
    lastSeen = top.id;
    if (!first && /render|delete|restore|publish|upload|create/.test(top.action)) load();
  });

  let headline = $derived.by(() => {
    if (!stats) return '';
    const r = stats.renders_today;
    const renders = r === 0 ? 'No documents rendered yet today.' : `${count(r, 'document')} rendered today.`;
    const errs = stats.calls_24h === 0 ? 'No calls in the last 24 hours.'
      : stats.errors_24h === 0 ? `All ${stats.calls_24h.toLocaleString()} calls of the last 24 hours went through.`
      : `${stats.errors_24h.toLocaleString()} of ${count(stats.calls_24h, 'call')} failed in the last 24 hours.`;
    return `${renders} ${errs}`;
  });
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Overview</h1>
      <p class="lead">{headline}</p>
    </div>
    <div class="actions">
      <a class="btn primary" href={href('/create')}>Create a document</a>
    </div>
  </div>

  {#if error}<p class="note error">{error}</p>{/if}

  {#if stats}
    <dl class="ledger" aria-label="Folio at a glance">
      <div><dt>Documents</dt><dd class="num">{stats.documents.toLocaleString()}</dd></div>
      <div><dt>Templates</dt><dd class="num">{stats.templates}</dd></div>
      <div><dt>Brands</dt><dd class="num">{stats.brands}</dd></div>
      <div><dt>Images</dt><dd class="num">{stats.assets}</dd></div>
      <div><dt>Errors, 24 h</dt><dd class="num" class:bad={stats.error_rate_24h > 0.1}>{percent(stats.error_rate_24h)}</dd></div>
      <div><dt>Stored</dt><dd class="num">{bytes(stats.storage_bytes)}</dd></div>
    </dl>
  {/if}

  <div class="split">
    <section aria-labelledby="proofs-h">
      <div class="section-head">
        <h2 id="proofs-h">Latest proofs</h2>
        <a href={href('/documents')}>All documents</a>
      </div>
      {#if docs.length === 0 && stats}
        <div class="sheet empty">
          <p><strong>No documents yet.</strong> Documents your agents render appear here as proofs, and so do the ones you create from Markdown.</p>
          <a class="btn" href={href('/create')}>Create a document</a>
        </div>
      {:else}
        <ul class="proofs">
          {#each docs as d (d.id)}
            <li>
              <a class="proof-link" href={href('/documents/' + d.id)}>
                <Proof src={d.preview_url} alt="First page of {d.title}" />
                <span class="cap">
                  <span class="title">{d.title || 'Untitled'}</span>
                  <span class="muted small">{d.client || d.template_id}, {ago(d.updated_at)}</span>
                </span>
              </a>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section aria-labelledby="live-h" class="sheet feed">
      <div class="section-head">
        <h2 id="live-h">Live activity</h2>
        <a href={href('/activity')}>All activity</a>
      </div>
      <ActivityList rows={live.rows.slice(0, 14)} fresh={live.fresh} compact />
    </section>
  </div>
</div>

<style>
  .lead {
    font-size: var(--fs-2);
    color: var(--ink);
    max-width: 60ch;
  }
  .ledger {
    display: flex;
    flex-wrap: wrap;
    margin: 0 0 var(--s-6);
    background: var(--paper);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
  }
  .ledger > div {
    flex: 1 1 8rem;
    padding: var(--s-3) var(--s-4);
    border-right: 1px solid var(--rule);
  }
  .ledger > div:last-child {
    border-right: 0;
  }
  dt {
    font-size: var(--fs-0);
    color: var(--ink-2);
    font-weight: 600;
  }
  dd {
    margin: 0;
    font-size: var(--fs-3);
    font-weight: 700;
  }
  dd.bad {
    color: var(--error);
  }
  .split {
    display: grid;
    grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr);
    gap: var(--s-6);
    align-items: start;
  }
  .section-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: var(--s-4);
  }
  .proofs {
    list-style: none;
    margin: 0;
    padding: var(--s-2) var(--s-2) 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
    gap: var(--s-6) var(--s-5);
  }
  .proof-link {
    display: grid;
    gap: var(--s-3);
    color: var(--ink);
    text-decoration: none;
  }
  .cap {
    display: grid;
  }
  .title {
    font-weight: 700;
    line-height: 1.25;
  }
  .empty {
    display: grid;
    gap: var(--s-4);
    justify-items: start;
  }
  .feed {
    padding-top: var(--s-4);
  }
  @media (max-width: 960px) {
    .split {
      grid-template-columns: 1fr;
    }
  }
  @media (max-width: 560px) {
    .proofs {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: var(--s-5) var(--s-4);
    }
  }
</style>
