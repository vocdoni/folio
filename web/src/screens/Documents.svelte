<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { api, query } from '../lib/api';
  import { router, href } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import type { DocumentSummary, TemplateSummary } from '../lib/types';
  import Proof from '../components/Proof.svelte';
  import DocumentDrawer from './DocumentDrawer.svelte';

  let { openId = '' }: { openId?: string } = $props();
  let q = $state(router.param('query'));
  let status = $state(router.param('status'));
  let template = $state(router.param('template'));
  let items = $state<DocumentSummary[]>([]);
  let cursor = $state('');
  let loading = $state(true);
  let error = $state('');
  let templates = $state<TemplateSummary[]>([]);

  async function load(more = false) {
    loading = true;
    error = '';
    try {
      const page = await api.get<{ items: DocumentSummary[]; next_cursor?: string }>(
        '/documents' + query({ query: q, status, template, cursor: more ? cursor : '', limit: 48 }),
      );
      items = more ? [...items, ...page.items] : page.items;
      cursor = page.next_cursor ?? '';
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const params = { query: q, status, template };
    clearTimeout(timer);
    timer = setTimeout(() => {
      if (router.route.name === 'documents') router.setParams(params);
      load();
    }, 250);
  });
  $effect(() => {
    api.get<{ items: TemplateSummary[] }>('/templates?include_archived=true').then((r) => (templates = r.items)).catch(() => {});
  });
  function closed(changed: boolean) {
    router.navigate('/documents' + query({ query: q, status, template }));
    if (changed) load();
  }
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Documents</h1>
      <p>Everything Folio rendered, newest first. Open a proof to read it page by page, download it or change its status.</p>
    </div>
    <div class="actions"><a class="btn primary" href={href('/create')}>Create a document</a></div>
  </div>

  <form class="filters" role="search" onsubmit={(e) => e.preventDefault()}>
    <div class="field grow">
      <label for="doc-q">Search titles and clients</label>
      <input id="doc-q" type="search" bind:value={q} placeholder="For example ACME or proposal" />
    </div>
    <div class="field">
      <label for="doc-status">Status</label>
      <select id="doc-status" bind:value={status}>
        <option value="">Drafts and published</option><option value="draft">Drafts</option>
        <option value="published">Published</option><option value="trashed">In the trash</option>
      </select>
    </div>
    <div class="field">
      <label for="doc-template">Template</label>
      <select id="doc-template" bind:value={template}>
        <option value="">Any template</option>
        {#each templates as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
      </select>
    </div>
  </form>

  {#if error}<p class="note error">{error}</p>{/if}

  {#if !loading && items.length === 0}
    <div class="sheet empty">
      {#if q || status || template}
        <p><strong>No documents match these filters.</strong> Clear the search or pick another status.</p>
        <button class="btn" onclick={() => { q = ''; status = ''; template = ''; }}>Clear filters</button>
      {:else}
        <p><strong>No documents yet.</strong> When an agent renders one through MCP it appears here. You can also create one from Markdown.</p>
        <a class="btn primary" href={href('/create')}>Create a document</a>
      {/if}
    </div>
  {:else}
    <ul class="grid" aria-busy={loading}>
      {#each items as d (d.id)}
        <li>
          <a class="card" href={href('/documents/' + d.id)}>
            <Proof src={d.preview_url} alt="First page of {d.title || 'an untitled document'}" />
            <span class="meta">
              <span class="title">{d.title || 'Untitled'}</span>
              <span class="sub muted small">{d.client || 'No client'}</span>
              <span class="row small">
                <span class="chip {d.status}">{d.status === 'draft' ? 'Draft' : d.status === 'published' ? 'Published' : 'In the trash'}</span>
                <span class="muted">rev. {d.latest_revision}, {ago(d.updated_at)}</span>
              </span>
            </span>
          </a>
        </li>
      {/each}
    </ul>
    {#if cursor}<button class="btn more" onclick={() => load(true)} disabled={loading}>Show more documents</button>{/if}
  {/if}
</div>

{#if openId}
  <DocumentDrawer id={openId} onclose={closed} />
{/if}

<style>
  .filters {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--s-4);
    margin-bottom: var(--s-5);
  }
  .grid {
    list-style: none;
    margin: 0;
    padding: var(--s-3) var(--s-3) 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: var(--s-7) var(--s-6);
  }
  .card {
    display: grid;
    gap: var(--s-3);
    color: var(--ink);
    text-decoration: none;
  }
  .meta {
    display: grid;
    gap: 0.1rem;
  }
  .title {
    font-weight: 700;
    line-height: 1.25;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--s-2);
    margin-top: var(--s-1);
  }
  .empty {
    display: grid;
    gap: var(--s-4);
    justify-items: start;
  }
  .more {
    margin-top: var(--s-6);
  }
  @media (max-width: 760px) {
    .filters {
      grid-template-columns: 1fr 1fr;
    }
    .grow {
      grid-column: 1 / -1;
    }
    .grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: var(--s-6) var(--s-4);
    }
  }
</style>
