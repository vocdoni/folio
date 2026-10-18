<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { api, query, ApiError } from '../lib/api';
  import { href } from '../lib/router.svelte';
  import { bytes, when } from '../lib/format';
  import { toasts } from '../lib/toast.svelte';
  import type { DocumentDetail } from '../lib/types';
  import Proof from '../components/Proof.svelte';
  import Findings from '../components/Findings.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Icon from '../components/Icon.svelte';
  import Copy from '../components/Copy.svelte';

  let { id, onclose }: { id: string; onclose: (changed: boolean) => void } = $props();
  let doc = $state<DocumentDetail | null>(null);
  let revision = $state(0);
  let page = $state(1);
  let error = $state('');
  let changed = false;
  let confirm = $state<'' | 'trash' | 'purge'>('');
  let busy = $state(false);
  let el: HTMLDialogElement | undefined = $state();

  async function load() {
    error = '';
    try {
      doc = await api.get<DocumentDetail>(`/documents/${encodeURIComponent(id)}` + query({ revision }));
      page = Math.min(page, Math.max(doc.page_urls.length, 1));
    } catch (e) {
      error = e instanceof ApiError && e.status === 404 ? 'This document does not exist any more.' : e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    void revision;
    load();
  });
  $effect(() => {
    if (el && !el.open) el.showModal();
  });

  async function act(label: string, fn: () => Promise<unknown>, done: string) {
    busy = true;
    try {
      await fn();
      changed = true;
      toasts.show(done);
      await load();
    } catch (e) {
      toasts.show(`${label} failed: ${e instanceof Error ? e.message : e}`, 'error');
    } finally {
      busy = false;
      confirm = '';
    }
  }
  const publish = () => act('Publishing', () => api.post(`/documents/${id}/publish`, { revision: doc!.revision.revision }), `Published revision ${doc!.revision.revision}.`);
  const restore = () => act('Restoring', () => api.post(`/documents/${id}/restore`), 'Restored from the trash.');
  const trash = () => act('Moving to the trash', () => api.del(`/documents/${id}?confirm_published=true`), 'Moved to the trash.');
  async function purge() {
    busy = true;
    try {
      await api.del(`/documents/${id}?purge=true&confirm_published=true`);
      toasts.show('Deleted permanently.');
      onclose(true);
    } catch (e) {
      toasts.show(`Deleting failed: ${e instanceof Error ? e.message : e}`, 'error');
    } finally {
      busy = false;
      confirm = '';
    }
  }
  let files = $derived((doc?.files.filter((f) => f.kind !== 'preview') ?? []).sort((a, b) => (a.kind === 'pdf' ? -1 : b.kind === 'pdf' ? 1 : 0)));
  let pdf = $derived(files.find((f) => f.kind === 'pdf'));
  let review = $derived(doc?.revision.review?.findings ?? []);
  function keys(e: KeyboardEvent) {
    if (!doc || (e.target as HTMLElement).closest('input, textarea, select')) return;
    if (e.key === 'ArrowRight' && page < doc.page_urls.length) page++;
    if (e.key === 'ArrowLeft' && page > 1) page--;
  }
</script>

<dialog bind:this={el} class="drawer" aria-labelledby="doc-title" onclose={() => onclose(changed)} onkeydown={keys}
  onclick={(e) => { if (e.target === el) el?.close(); }}>
  <div class="panel">
    <header>
      <div>
        <h2 id="doc-title">{doc?.title || (error ? 'Document' : 'Loading…')}</h2>
        {#if doc}
          <p class="muted small">
            {doc.client || 'No client'}, template {doc.template_id}, revision {doc.revision.revision} of {doc.latest_revision}
          </p>
        {/if}
      </div>
      <button class="btn quiet" onclick={() => el?.close()} aria-label="Close"><Icon name="close" /></button>
    </header>

    {#if error}
      <p class="note error">{error}</p>
    {:else if doc}
      <div class="status-row">
        <span class="chip {doc.status}">{doc.status === 'draft' ? 'Draft' : doc.status === 'published' ? `Published (revision ${doc.published_revision})` : 'In the trash'}</span>
      </div>

      <section class="viewer" aria-label="Pages">
        <div class="stage">
          <Proof src={doc.page_urls[page - 1] ?? ''} alt="Page {page} of {doc.title}" eager marked
            blank={doc.page_urls.length ? 'Loading the page' : 'This revision has no PDF pages (DOCX only)'} />
        </div>
        {#if doc.page_urls.length > 1}
          <div class="pager">
            <button class="btn small" onclick={() => page--} disabled={page <= 1} aria-label="Previous page"><Icon name="left" /></button>
            <span class="num" aria-live="polite">Page {page} of {doc.page_urls.length}</span>
            <button class="btn small" onclick={() => page++} disabled={page >= doc.page_urls.length} aria-label="Next page"><Icon name="right" /></button>
          </div>
        {/if}
      </section>

      <section class="block">
        <h3>Download</h3>
        {#if doc.status === 'trashed'}
          <p class="muted">Downloads are switched off while the document is in the trash. Restore it to share it again.</p>
        {:else}
          <div class="buttons">
            {#each files as f (f.url)}
              <a class="btn" href={f.url} download={f.name}><Icon name="download" />Download {f.kind.toUpperCase()} <span class="muted small">{bytes(f.size)}</span></a>
            {/each}
          </div>
          {#if pdf}<Copy text={pdf.url} label="Shareable PDF link" />
            <p class="muted small">Anyone with this link can download the PDF.</p>{/if}
        {/if}
      </section>

      <section class="block">
        <h3>Status</h3>
        <div class="buttons">
          {#if doc.status !== 'trashed'}
            {#if doc.published_revision !== doc.revision.revision}
              <button class="btn primary" onclick={publish} disabled={busy}><Icon name="check" />Publish revision {doc.revision.revision}</button>
            {/if}
            <a class="btn" href={href('/create?from=' + encodeURIComponent(doc.id))}>Revise</a>
            <button class="btn danger" onclick={() => (confirm = 'trash')} disabled={busy}><Icon name="trash" />Move to trash</button>
          {:else}
            <button class="btn" onclick={restore} disabled={busy}><Icon name="restore" />Restore</button>
            <button class="btn danger" onclick={() => (confirm = 'purge')} disabled={busy}>Delete permanently</button>
          {/if}
        </div>
      </section>

      {#if doc.revision.warnings?.length}
        <section class="block"><Findings findings={doc.revision.warnings} title="Warnings from the render" /></section>
      {/if}
      {#if review.length}
        <section class="block">
          <h3>Layout review</h3>
          <ul class="review">
            {#each review as f, i (i)}<li><span class="chip">page {f.page}</span> {f.message}</li>{/each}
          </ul>
        </section>
      {/if}

      <section class="block">
        <h3>Revisions</h3>
        <ol class="revs" reversed>
          {#each doc.revisions as r (r.revision)}
            <li>
              <button class="rev" class:current={r.revision === doc.revision.revision} onclick={() => (revision = r.revision)}
                aria-current={r.revision === doc.revision.revision ? 'true' : undefined}>
                <span class="num">Revision {r.revision}</span>
                <span class="muted small">{r.created_by}, {when(r.created_at)}, template {r.template.id} v{r.template.version}</span>
              </button>
            </li>
          {/each}
        </ol>
      </section>
    {/if}
  </div>
</dialog>

<Dialog bind:open={() => confirm === 'trash', (v) => { if (!v) confirm = ''; }} title="Move to the trash?" confirm="Move to trash" danger {busy} onconfirm={trash}>
  <p>Its download links stop working at once. You can restore it from the trash until it is purged automatically.</p>
</Dialog>
<Dialog bind:open={() => confirm === 'purge', (v) => { if (!v) confirm = ''; }} title="Delete permanently?" confirm="Delete permanently" danger {busy} onconfirm={purge}>
  <p>All revisions and files of “{doc?.title}” are deleted. This cannot be undone.</p>
</Dialog>

<style>
  .drawer {
    margin: 0 0 0 auto;
    height: 100vh;
    max-height: 100vh;
    width: min(36rem, 100vw);
    max-width: 100vw;
    border: 0;
    padding: 0;
    background: var(--paper);
    box-shadow: -12px 0 30px -18px rgba(28, 36, 33, 0.5);
  }
  .drawer::backdrop {
    background: rgba(28, 36, 33, 0.35);
  }
  .panel {
    padding: var(--s-5);
    display: grid;
    gap: var(--s-5);
  }
  header {
    display: flex;
    justify-content: space-between;
    gap: var(--s-3);
    align-items: flex-start;
  }
  h2 {
    font-size: var(--fs-4);
  }
  .viewer {
    background: var(--table);
    margin: 0 calc(-1 * var(--s-5));
    padding: var(--s-6) var(--s-6) var(--s-4);
    display: grid;
    gap: var(--s-4);
    justify-items: center;
  }
  .stage {
    width: min(100%, 26rem);
  }
  .pager {
    display: flex;
    align-items: center;
    gap: var(--s-3);
  }
  .block {
    display: grid;
    gap: var(--s-3);
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s-2);
  }
  .revs,
  .review {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--s-1);
  }
  .rev {
    width: 100%;
    display: grid;
    text-align: left;
    gap: 0.1rem;
    padding: var(--s-2) var(--s-3);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    font: inherit;
    color: inherit;
    cursor: pointer;
  }
  .rev:hover {
    background: var(--paper-2);
  }
  .rev.current {
    border-color: var(--rule-strong);
    background: var(--paper-2);
  }
</style>
