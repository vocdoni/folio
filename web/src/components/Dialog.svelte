<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- A modal dialog built on <dialog> (focus trap, Esc and focus return for free). -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  let {
    open = $bindable(false),
    title,
    confirm,
    danger = false,
    busy = false,
    disabled = false,
    onconfirm,
    children,
  }: {
    open?: boolean;
    title: string;
    confirm: string;
    danger?: boolean;
    /** The action is running: the confirm button waits. */
    busy?: boolean;
    /** The form is not ready to confirm (a required field is empty). */
    disabled?: boolean;
    onconfirm: () => void;
    children?: Snippet;
  } = $props();
  const uid = $props.id();
  let el: HTMLDialogElement | undefined = $state();
  $effect(() => {
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  });
</script>

<dialog bind:this={el} onclose={() => (open = false)} aria-labelledby="{uid}-title">
  <form method="dialog" class="sheet">
    <h2 id="{uid}-title">{title}</h2>
    <div class="body">{@render children?.()}</div>
    <div class="buttons">
      <button class="btn" value="cancel" formnovalidate>Cancel</button>
      <button class="btn {danger ? 'danger solid' : 'primary'}" type="button" disabled={busy || disabled} onclick={onconfirm}>{confirm}</button>
    </div>
  </form>
</dialog>

<style>
  dialog {
    border: 0;
    padding: 0;
    background: transparent;
    max-width: min(32rem, calc(100vw - 2rem));
    width: 100%;
  }
  dialog::backdrop {
    background: rgba(28, 36, 33, 0.45);
  }
  .sheet {
    box-shadow: var(--sheet-shadow);
  }
  .body {
    margin: var(--s-3) 0 var(--s-5);
    color: var(--ink-2);
    display: grid;
    gap: var(--s-2);
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s-2);
  }
</style>
