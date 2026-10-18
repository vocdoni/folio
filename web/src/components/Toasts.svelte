<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { toasts } from '../lib/toast.svelte';
  import Icon from './Icon.svelte';
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts.items as t (t.id)}
    <div class="toast" class:error={t.kind === 'error'}>
      <span>{t.text}</span>
      <button class="btn quiet small" onclick={() => toasts.dismiss(t.id)} aria-label="Dismiss"><Icon name="close" /></button>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    left: var(--s-4);
    bottom: var(--s-4);
    z-index: 60;
    display: grid;
    gap: var(--s-2);
    max-width: min(28rem, calc(100vw - 2rem));
  }
  .toast {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s-3);
    padding: var(--s-2) var(--s-2) var(--s-2) var(--s-4);
    background: var(--ink);
    color: var(--paper);
    border-left: 4px solid var(--cyan);
    border-radius: var(--radius-control);
    box-shadow: var(--sheet-shadow);
  }
  .toast.error {
    border-left-color: var(--error);
  }
  .toast .btn {
    color: var(--paper);
  }
  .toast .btn:hover {
    background: rgba(255, 255, 255, 0.12);
  }
</style>
