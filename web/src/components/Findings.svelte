<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Validation findings: what to fix, where, and how. -->
<script lang="ts">
  import type { Finding } from '../lib/types';
  let { findings, title = '' }: { findings: Finding[]; title?: string } = $props();
  let errors = $derived(findings.filter((f) => f.severity === 'error'));
</script>

{#if findings.length}
  <div class="note" class:error={errors.length > 0} class:warn={errors.length === 0}>
    {#if title}<p class="title">{title}</p>{/if}
    <ul>
      {#each findings as f, i (i)}
        <li>
          <span>{f.message}</span>
          {#if f.path}<span class="where">at {f.path}{#if f.page}, page {f.page}{/if}</span>{/if}
          {#if f.hint}<span class="hint">{f.hint}</span>{/if}
        </li>
      {/each}
    </ul>
  </div>
{/if}

<style>
  .title {
    font-weight: 700;
    margin-bottom: var(--s-1);
  }
  ul {
    margin: 0;
    padding-left: 1.1rem;
    display: grid;
    gap: var(--s-1);
  }
  .where,
  .hint {
    display: block;
    font-size: var(--fs-0);
    color: var(--ink-2);
  }
  .where {
    font-family: var(--mono);
  }
</style>
