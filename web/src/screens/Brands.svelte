<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, query } from '../lib/api';
  import { href } from '../lib/router.svelte';
  import type { BrandSummary } from '../lib/types';
  import Icon from '../components/Icon.svelte';

  let items = $state<BrandSummary[]>([]);
  let archived = $state(false);
  let loading = $state(true);
  async function load() {
    loading = true;
    items = (await api.get<{ items: BrandSummary[] }>('/brands' + query({ include_archived: archived }))).items;
    loading = false;
  }
  onMount(load);
  $effect(() => {
    void archived;
    load();
  });
  const roles = ['primary', 'secondary', 'accent', 'text', 'muted', 'background'] as const;
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>Brands</h1>
      <p>Colours, fonts, logo, header and footer that templates apply to every page.</p>
    </div>
    <div class="actions"><a class="btn primary" href={href('/brands/new')}><Icon name="plus" />New brand</a></div>
  </div>
  <label class="check toggle"><input type="checkbox" bind:checked={archived} />Show archived brands</label>
  {#if !loading && items.length === 0}
    <div class="sheet empty"><p><strong>No brands yet.</strong> Every template needs one. Create it from your colours and logo{''}.</p>
      <a class="btn primary" href={href('/brands/new')}>New brand</a></div>
  {:else}
    <ul class="specimens" aria-busy={loading}>
      {#each items as b (b.id)}
        <li>
          <a class="specimen" href={href('/brands/' + b.id)}>
            <span class="head">
              {#if b.spec.logo}<img src={`/ui/api/assets/${b.spec.logo.asset_id}/content`} alt="" class="logo" />{/if}
              <span class="name">{b.spec.name}</span>
              {#if b.archived}<span class="chip trashed">Archived</span>{/if}
            </span>
            <span class="swatches">
              {#each roles as r (r)}
                <span class="sw" title="{r} {b.spec.colors[r]}"><i style:background={b.spec.colors[r]}></i><span class="small">{r}</span></span>
              {/each}
            </span>
            <span class="small muted">{b.spec.fonts.heading} and {b.spec.fonts.body}, version {b.version}</span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .toggle {
    margin-bottom: var(--s-5);
  }
  .specimens {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(19rem, 1fr));
    gap: var(--s-5);
  }
  .specimen {
    display: grid;
    gap: var(--s-4);
    padding: var(--s-5);
    background: var(--paper);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    color: var(--ink);
    text-decoration: none;
  }
  .specimen:hover {
    border-color: var(--rule-strong);
  }
  .head {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    min-height: 2.5rem;
  }
  .logo {
    max-height: 2.25rem;
    max-width: 7rem;
    object-fit: contain;
  }
  .name {
    font-size: var(--fs-2);
    font-weight: 750;
    flex: 1;
  }
  .swatches {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--s-1);
  }
  .sw {
    display: grid;
    gap: 2px;
  }
  .sw i {
    display: block;
    height: 2.25rem;
    border: 1px solid var(--rule);
  }
  .sw .small {
    font-size: 0.6875rem;
    color: var(--ink-2);
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .empty {
    display: grid;
    gap: var(--s-4);
    justify-items: start;
  }
</style>
