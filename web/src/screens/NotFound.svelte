<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { href } from '../lib/router.svelte';
</script>

<div class="page">
  <div class="page-head"><div><h1>Nothing here</h1><p>This address does not match a page of Folio.</p></div></div>
  <a class="btn" href={href('/')}>Go to the overview</a>
</div>
