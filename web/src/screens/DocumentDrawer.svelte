<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  let { id = '', onclose }: { id?: string; onclose?: () => void } = $props();
</script>

<div class="page"><div class="page-head"><div><h1>Document</h1><p>{id}</p></div><button class="btn" onclick={onclose}>Close</button></div></div>
