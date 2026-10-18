<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, query } from '../lib/api';
  import { bytes, date } from '../lib/format';
  import { toasts } from '../lib/toast.svelte';
  import { session } from '../lib/session.svelte';
  import type { Asset } from '../lib/types';
  import Icon from '../components/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Copy from '../components/Copy.svelte';

  let items = $state<Asset[]>([]);
  let cursor = $state('');
  let q = $state('');
  let loading = $state(true);
  let uploading = $state(false);
  let error = $state('');
  let doomed = $state<Asset | null>(null);

  async function load(more = false) {
    loading = true;
    try {
      const page = await api.get<{ items: Asset[]; next_cursor?: string }>('/assets' + query({ query: q, cursor: more ? cursor : '', limit: 60 }));
      items = more ? [...items, ...page.items] : page.items;
      cursor = page.next_cursor ?? '';
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  onMount(load);
  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    void q;
    clearTimeout(timer);
    timer = setTimeout(() => load(), 250);
  });

  async function upload(files: FileList | null | undefined) {
    if (!files?.length) return;
    uploading = true;
    const max = session.caps?.limits.max_asset_bytes ?? Infinity;
    for (const f of Array.from(files)) {
      if (f.size > max) {
        toasts.show(`${f.name} is larger than ${bytes(max)}. Shrink it and try again.`, 'error');
        continue;
      }
      const fd = new FormData();
      fd.append('file', f);
      try {
        const a = await api.post<Asset>('/assets', fd);
        items = [a, ...items];
        toasts.show(`Uploaded ${a.name}.`);
      } catch (e) {
        toasts.show(`${f.name}: ${e instanceof Error ? e.message : e}`, 'error');
      }
    }
    uploading = false;
  }
  async function remove() {
    const a = doomed!;
    try {
      await api.del(`/assets/${a.asset_id}`);
      items = items.filter((x) => x.asset_id !== a.asset_id);
      toasts.show(`Deleted ${a.name}.`);
    } catch (e) {
      toasts.show(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      doomed = null;
    }
  }
  let dragging = $state(false);
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Images</h1>
      <p>Logos and pictures for brands and documents. Folio cleans every file (SVG scripts are removed) and stores PNG, JPEG and SVG.</p>
    </div>
    <div class="actions">
      <label class="btn primary upload"><Icon name="upload" />{uploading ? 'Uploading…' : 'Upload images'}
        <input type="file" multiple accept="image/png,image/jpeg,image/svg+xml,image/webp" class="visually-hidden" onchange={(e) => upload((e.currentTarget as HTMLInputElement).files)} disabled={uploading} />
      </label>
    </div>
  </div>

  <div class="drop" class:dragging role="region" aria-label="Drop images here to upload them"
    ondragover={(e) => { e.preventDefault(); dragging = true; }} ondragleave={() => (dragging = false)}
    ondrop={(e) => { e.preventDefault(); dragging = false; upload(e.dataTransfer?.files); }}>
    <div class="field search">
      <label for="a-q">Search names and descriptions</label>
      <input id="a-q" type="search" bind:value={q} />
    </div>
    {#if error}<p class="note error">{error}</p>{/if}
    {#if !loading && items.length === 0}
      <div class="sheet empty">
        <p><strong>{q ? 'No images match.' : 'No images yet.'}</strong> {q ? 'Try another word.' : 'Upload a logo to use it in a brand, or drop image files anywhere on this page.'}</p>
      </div>
    {:else}
      <ul class="tiles" aria-busy={loading}>
        {#each items as a (a.asset_id)}
          <li class="tile">
            <span class="img"><img src={a.url} alt={a.description || a.name} loading="lazy" /></span>
            <span class="name">{a.name}</span>
            <span class="small muted">{a.mime.replace('image/', '').replace('svg+xml', 'SVG').toUpperCase()}, {a.width}×{a.height}, {bytes(a.size)}, {date(a.created_at)}</span>
            <Copy text={`![${a.description || a.name}](asset:${a.asset_id})`} label="Markdown to use this image" />
            <button class="btn danger small" onclick={() => (doomed = a)}><Icon name="trash" />Delete</button>
          </li>
        {/each}
      </ul>
      {#if cursor}<button class="btn more" onclick={() => load(true)} disabled={loading}>Show more images</button>{/if}
    {/if}
  </div>
</div>

<Dialog bind:open={() => doomed !== null, (v) => { if (!v) doomed = null; }} title="Delete this image?" confirm="Delete" danger onconfirm={remove}>
  <p>“{doomed?.name}” disappears from this list and cannot be used in new documents. Brands and documents that already use it keep it.</p>
</Dialog>

<style>
  .upload {
    cursor: pointer;
  }
  .upload:focus-within {
    box-shadow: var(--focus);
  }
  .drop {
    border: 2px dashed transparent;
    border-radius: var(--radius);
    margin: calc(-1 * var(--s-3));
    padding: var(--s-3);
  }
  .drop.dragging {
    border-color: var(--magenta);
    background: var(--magenta-tint);
  }
  .search {
    max-width: 28rem;
  }
  .tiles {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr));
    gap: var(--s-5);
  }
  .tile {
    display: grid;
    gap: var(--s-2);
    align-content: start;
    background: var(--paper);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    padding: var(--s-3);
  }
  .img {
    display: grid;
    place-items: center;
    aspect-ratio: 4 / 3;
    /* Checkerboard so transparent logos stay visible. */
    background:
      repeating-conic-gradient(#eef1ef 0% 25%, #ffffff 0% 50%) 50% / 16px 16px;
    border: 1px solid var(--rule);
  }
  .img img {
    max-width: 85%;
    max-height: 85%;
    object-fit: contain;
  }
  .name {
    font-weight: 700;
    overflow-wrap: anywhere;
  }
  .tile .btn.danger {
    justify-self: start;
  }
  .empty {
    display: grid;
    gap: var(--s-3);
  }
  .more {
    margin-top: var(--s-5);
  }
</style>
