<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, fileToBase64, query } from '../lib/api';
  import { href, router } from '../lib/router.svelte';
  import { session } from '../lib/session.svelte';
  import { work } from '../lib/work.svelte';
  import { runJob } from '../lib/jobs';
  import { handoff } from '../lib/handoff';
  import type { DraftResult, TemplateSummary } from '../lib/types';
  import Icon from '../components/Icon.svelte';

  let items = $state<TemplateSummary[]>([]);
  let archived = $state(false);
  let loading = $state(true);
  let error = $state('');
  let drafting = $state(false);
  let description = $state('');
  let example: File | null = $state(null);
  let step = $state('');
  let draftError = $state('');

  async function load() {
    loading = true;
    try {
      items = (await api.get<{ items: TemplateSummary[] }>('/templates' + query({ include_archived: archived }))).items;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  onMount(load);
  $effect(() => {
    void archived;
    load();
  });

  async function draft(e: SubmitEvent) {
    e.preventDefault();
    draftError = '';
    step = 'starting';
    try {
      const body: Record<string, string> = { description };
      if (example) {
        body.example_name = example.name;
        body.example_base64 = await fileToBase64(example);
      }
      handoff.draft = await work.run(runJob<DraftResult>('/ai/template-draft', body, (s) => (step = s)));
      session.refreshAI();
      router.navigate('/templates/new');
    } catch (err) {
      draftError = err instanceof Error ? err.message : String(err);
    } finally {
      step = '';
    }
  }
  const accept = $derived(session.ai.vision ? '.md,.markdown,.txt,.docx,.pdf' : '.md,.markdown,.txt,.docx');
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Templates</h1>
      <p>A template fixes a document’s sections, details, tables and look. Agents and the create screen fill it with text.</p>
    </div>
    <div class="actions">
      {#if session.ai.enabled}
        <button class="btn" onclick={() => (drafting = !drafting)} aria-expanded={drafting}><Icon name="assistant" />Draft with the assistant</button>
      {/if}
      <a class="btn primary" href={href('/templates/new')}><Icon name="plus" />New template</a>
    </div>
  </div>

  {#if drafting}
    <form class="sheet draft" onsubmit={draft} aria-labelledby="draft-h">
      <h2 id="draft-h" class="sheet-title">Describe the template you need</h2>
      <div class="field">
        <label for="d-desc">What documents is it for?</label>
        <textarea id="d-desc" rows="4" maxlength="8192" bind:value={description}
          placeholder="A one-page quote for IT hardware: client details, a line-item budget with VAT, payment terms and a signature block."></textarea>
      </div>
      <div class="field">
        <label for="d-file">Example document (optional)</label>
        <input id="d-file" type="file" {accept} onchange={(e) => (example = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} />
        <span class="help">Markdown, text or Word{session.ai.vision ? ', or a PDF (the first four pages are read)' : ''}. The assistant copies its structure, not its words.</span>
      </div>
      <p class="small muted">Your description and example are sent to the configured AI model. The draft opens in the studio unsaved.</p>
      {#if step}<p class="note" role="status">The assistant is {step}.</p>{/if}
      {#if draftError}<p class="note error" role="alert">{draftError}</p>{/if}
      <button class="btn primary" type="submit" disabled={!!step || (!description.trim() && !example)}>{step ? 'Drafting…' : 'Draft the template'}</button>
    </form>
  {/if}

  <label class="check toggle"><input type="checkbox" bind:checked={archived} />Show archived templates</label>
  {#if error}<p class="note error">{error}</p>{/if}

  {#if !loading && items.length === 0}
    <div class="sheet empty">
      <p><strong>No templates{archived ? '' : ' in use'}.</strong> Start from scratch in the studio{session.ai.enabled ? ' or let the assistant draft one from a description' : ''}.</p>
      <a class="btn primary" href={href('/templates/new')}>New template</a>
    </div>
  {:else}
    <ul class="shelf" aria-busy={loading}>
      {#each items as t (t.id)}
        <li>
          <a class="contents" href={href('/templates/' + t.id)}>
            <span class="top">
              <span class="name">{t.name}</span>
              {#if t.archived}<span class="chip trashed">Archived</span>{/if}
            </span>
            <span class="desc">{t.description}</span>
            <ol class="toc">
              {#each t.sections.slice(0, 9) as s, i (i)}
                <li><span class="sec">{s.title}</span>{#if s.component}<span class="comp">{s.component}</span>{/if}</li>
              {/each}
              {#if t.sections.length > 9}<li class="more">and {t.sections.length - 9} more</li>{/if}
            </ol>
            <span class="foot small muted">{t.layout} layout, {t.languages.join(', ')}, brand {t.brand_id}, version {t.version}</span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .draft {
    margin-bottom: var(--s-6);
    max-width: 48rem;
    box-shadow: var(--sheet-shadow);
  }
  .toggle {
    margin-bottom: var(--s-5);
  }
  .shelf {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(17rem, 1fr));
    gap: var(--s-5);
  }
  /* A contents page: the template's sections listed like a table of contents. */
  .contents {
    display: grid;
    grid-template-rows: auto auto 1fr auto;
    gap: var(--s-2);
    height: 100%;
    padding: var(--s-5) var(--s-5) var(--s-4);
    background: var(--paper);
    border: 1px solid var(--rule);
    border-top: 5px solid var(--ink);
    border-radius: var(--radius);
    color: var(--ink);
    text-decoration: none;
  }
  .contents:hover {
    border-color: var(--rule-strong);
    border-top-color: var(--magenta);
  }
  .top {
    display: flex;
    justify-content: space-between;
    gap: var(--s-2);
    align-items: baseline;
  }
  .name {
    font-size: var(--fs-2);
    font-weight: 750;
    line-height: 1.2;
  }
  .desc {
    color: var(--ink-2);
    font-size: var(--fs-0);
  }
  .toc {
    margin: var(--s-2) 0;
    padding: var(--s-2) 0 0 1.4rem;
    border-top: 1px solid var(--rule);
    font-size: var(--fs-0);
    display: grid;
    gap: 0.15rem;
  }
  .toc li::marker {
    color: var(--ink-2);
  }
  .toc li {
    display: list-item;
  }
  .sec {
    margin-right: var(--s-2);
  }
  .comp {
    font-size: 0.75rem;
    font-weight: 700;
    padding: 0 0.35em;
    border: 1px solid var(--magenta);
    color: var(--magenta-deep);
    border-radius: 2px;
  }
  .more {
    list-style: none;
    color: var(--ink-2);
  }
  .empty {
    display: grid;
    gap: var(--s-4);
    justify-items: start;
  }
</style>
