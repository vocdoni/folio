<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- A snippet with a copy button (connection commands, MCP URL). -->
<script lang="ts">
  import Icon from './Icon.svelte';
  // display: what is shown when it differs from what is copied (a masked token).
  let { text, label, display }: { text: string; label: string; display?: string } = $props();
  let copied = $state(false);
  async function copy() {
    await navigator.clipboard.writeText(text);
    copied = true;
    setTimeout(() => (copied = false), 1600);
  }
</script>

<div class="snippet">
  <pre aria-label={label}><code>{display ?? text}</code></pre>
  <button class="btn small" aria-label={`${copied ? 'Copied' : 'Copy'} ${label}`} onclick={copy}><Icon name={copied ? 'check' : 'copy'} />{copied ? 'Copied' : 'Copy'}</button>
</div>

<style>
  .snippet {
    display: flex;
    align-items: flex-start;
    gap: var(--s-2);
    background: var(--paper-2);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    padding: var(--s-2) var(--s-2) var(--s-2) var(--s-3);
  }
  pre {
    flex: 1;
    margin: 0;
    overflow-x: auto;
    font-family: var(--mono);
    font-size: var(--fs-0);
    line-height: 1.6;
    white-space: pre-wrap;
    word-break: break-all;
  }
</style>
