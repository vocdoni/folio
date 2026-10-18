<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- The pi coding agent: user-level MCP registration and skill. -->
<script lang="ts">
  import Copy from './Copy.svelte';
  import { piSetup } from '../lib/connect';
  // token is what is copied; shown is what is displayed (masked while hidden).
  let { base, token, shown }: { base: string; token: string | null; shown: string | null } = $props();
  const real = $derived(piSetup(base, token));
  const display = $derived(piSetup(base, shown));
</script>

<p class="small muted">Run these in a terminal. In a pi session that is already open, run <span class="code">/reload</span> afterwards; <span class="code">/skill:folio</span> forces the skill.</p>
<Copy text={real.commands} display={display.commands} label="pi setup commands" />
<p class="small muted">To edit the file yourself, put this in <span class="code">~/.pi/agent/mcp.json</span>:</p>
<Copy text={real.json} label="pi configuration file" />
