<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Raw JSON for values outside the form subset; checked when you leave it. -->
<script lang="ts">
  let { id, value = $bindable(), reason = '', rows = 5 }: { id: string; value: unknown; reason?: string; rows?: number } = $props();
  let text = $state(value === undefined ? '' : JSON.stringify(value, null, 2));
  let error = $state('');
  function commit() {
    if (text.trim() === '') {
      value = undefined;
      error = '';
      return;
    }
    try {
      value = JSON.parse(text);
      error = '';
    } catch (e) {
      error = 'This is not valid JSON: ' + (e instanceof Error ? e.message : String(e));
    }
  }
</script>

<textarea {id} class="code" {rows} bind:value={text} onblur={commit} aria-invalid={error ? 'true' : undefined} spellcheck="false"></textarea>
{#if reason}<span class="help">Edited as JSON: {reason}.</span>{/if}
{#if error}<span class="error-text" role="alert">{error}</span>{/if}
