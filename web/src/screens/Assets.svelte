<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, query, updateAsset } from '../lib/api';
  import { descriptionProblem, nameProblem, rowsValid, suggestedName } from '../lib/assetNames';
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

  interface Row {
    file: File;
    preview: string;
    name: string;
    description: string;
  }
  let rows = $state<Row[]>([]);
  let naming = $state(false);

  /** choose opens the naming dialog for the picked or dropped files. */
  function choose(files: FileList | null | undefined) {
    const picked = Array.from(files ?? []);
    if (!picked.length) return;
    const max = session.caps?.limits.max_asset_bytes ?? Infinity;
    const ok: Row[] = [];
    for (const f of picked) {
      if (f.size > max) {
        toasts.show(`${f.name} is larger than ${bytes(max)}. Shrink it and try again.`, 'error');
        continue;
      }
      ok.push({ file: f, preview: URL.createObjectURL(f), name: suggestedName(f.name), description: '' });
    }
    if (!ok.length) return;
    discard();
    rows = ok;
    naming = true;
  }
  function discard() {
    for (const r of rows) URL.revokeObjectURL(r.preview);
    rows = [];
    naming = false;
  }
  async function upload() {
    if (!rowsValid(rows)) return;
    const batch = rows;
    naming = false;
    uploading = true;
    for (const r of batch) {
      const fd = new FormData();
      fd.append('file', r.file);
      fd.append('name', r.name.trim());
      fd.append('description', r.description.trim());
      try {
        const a = await api.post<Asset>('/assets', fd);
        items = [a, ...items];
        toasts.show(`Uploaded ${a.name}.`);
      } catch (e) {
        toasts.show(`${r.name}: ${e instanceof Error ? e.message : e}`, 'error');
      }
    }
    discard();
    uploading = false;
  }
  function onEnter(e: KeyboardEvent, go: () => void) {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    go();
  }

  let editing = $state<Asset | null>(null);
  let editName = $state('');
  let editDescription = $state('');
  let saving = $state(false);
  function edit(a: Asset) {
    editName = a.name;
    editDescription = a.description ?? '';
    editing = a;
  }
  async function save() {
    const a = editing!;
    if (nameProblem(editName) || descriptionProblem(editDescription)) return;
    saving = true;
    try {
      const updated = await updateAsset(a.asset_id, editName.trim(), editDescription.trim());
      items = items.map((x) => (x.asset_id === updated.asset_id ? updated : x));
      toasts.show('Saved.');
      editing = null;
    } catch (e) {
      toasts.show(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      saving = false;
    }
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
        <input type="file" multiple accept="image/png,image/jpeg,image/svg+xml,image/webp" class="visually-hidden" onchange={(e) => { const input = e.currentTarget as HTMLInputElement; choose(input.files); input.value = ''; }} disabled={uploading} />
      </label>
    </div>
  </div>

  <div class="drop" class:dragging role="region" aria-label="Drop images here to upload them"
    ondragover={(e) => { e.preventDefault(); dragging = true; }} ondragleave={() => (dragging = false)}
    ondrop={(e) => { e.preventDefault(); dragging = false; choose(e.dataTransfer?.files); }}>
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
            {#if a.description}<span class="small description">{a.description}</span>{/if}
            <span class="actions">
              <button class="btn small" onclick={() => edit(a)} aria-label={`Edit ${a.name}`}><Icon name="edit" />Edit</button>
              <button class="btn danger small" onclick={() => (doomed = a)} aria-label={`Delete ${a.name}`}><Icon name="trash" />Delete</button>
            </span>
          </li>
        {/each}
      </ul>
      {#if cursor}<button class="btn more" onclick={() => load(true)} disabled={loading}>Show more images</button>{/if}
    {/if}
  </div>
</div>

<Dialog bind:open={() => naming, (v) => { if (!v && naming) discard(); }} title="Name your images" confirm={rows.length > 1 ? 'Upload images' : 'Upload image'} disabled={!rowsValid(rows)} onconfirm={upload}>
  <p>Pick a name you will recognise in lists and a short description for people who cannot see the image.</p>
  <ul class="naming">
    {#each rows as r, i (r.preview)}
      {@const np = nameProblem(r.name)}
      {@const dp = descriptionProblem(r.description)}
      <li>
        <fieldset class="row">
          <legend>{r.file.name}</legend>
          <img class="thumb" src={r.preview} alt="" />
          <div class="fields">
            <div class="field">
              <label for="n-name-{i}">Name</label>
              <input id="n-name-{i}" type="text" bind:value={r.name} aria-invalid={np ? 'true' : undefined} aria-describedby={np ? `n-name-err-${i}` : undefined}
                onkeydown={(e) => onEnter(e, upload)} />
              {#if np}<span class="error-text" id="n-name-err-{i}">{np}</span>{/if}
            </div>
            <div class="field">
              <label for="n-desc-{i}">Describe the image for people who cannot see it (optional)</label>
              <input id="n-desc-{i}" type="text" bind:value={r.description} aria-invalid={dp ? 'true' : undefined} aria-describedby={dp ? `n-desc-err-${i}` : undefined}
                onkeydown={(e) => onEnter(e, upload)} />
              {#if dp}<span class="error-text" id="n-desc-err-{i}">{dp}</span>{/if}
            </div>
          </div>
        </fieldset>
      </li>
    {/each}
  </ul>
</Dialog>

<Dialog bind:open={() => editing !== null, (v) => { if (!v) editing = null; }} title="Edit image" confirm="Save" busy={saving}
  disabled={!!nameProblem(editName) || !!descriptionProblem(editDescription)} onconfirm={save}>
  {#if editing}
    {@const np = nameProblem(editName)}
    {@const dp = descriptionProblem(editDescription)}
    <div class="field">
      <label for="e-name">Name</label>
      <input id="e-name" type="text" bind:value={editName} aria-invalid={np ? 'true' : undefined} aria-describedby={np ? 'e-name-err' : undefined}
        onkeydown={(e) => onEnter(e, save)} />
      {#if np}<span class="error-text" id="e-name-err">{np}</span>{/if}
    </div>
    <div class="field">
      <label for="e-desc">Describe the image for people who cannot see it (optional)</label>
      <input id="e-desc" type="text" bind:value={editDescription} aria-invalid={dp ? 'true' : undefined} aria-describedby={dp ? 'e-desc-err' : undefined}
        onkeydown={(e) => onEnter(e, save)} />
      {#if dp}<span class="error-text" id="e-desc-err">{dp}</span>{/if}
    </div>
  {/if}
</Dialog>

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
  .description {
    color: var(--ink-2);
    overflow-wrap: anywhere;
  }
  .tile .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s-2);
  }
  .naming {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--s-4);
    max-height: 50vh;
    overflow-y: auto;
  }
  .row {
    display: grid;
    grid-template-columns: 5rem 1fr;
    gap: var(--s-3);
    align-items: start;
    margin: 0;
    padding: var(--s-3);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    min-width: 0;
  }
  .row legend {
    padding: 0 var(--s-1);
    font-size: var(--fs-0);
    color: var(--ink-2);
    overflow-wrap: anywhere;
  }
  .thumb {
    width: 5rem;
    height: 5rem;
    object-fit: contain;
    border: 1px solid var(--rule);
    background: repeating-conic-gradient(#eef1ef 0% 25%, #ffffff 0% 50%) 50% / 12px 12px;
  }
  .fields {
    min-width: 0;
  }
  @media (max-width: 30rem) {
    .row {
      grid-template-columns: 1fr;
    }
  }
  .empty {
    display: grid;
    gap: var(--s-3);
  }
  .more {
    margin-top: var(--s-5);
  }
</style>
