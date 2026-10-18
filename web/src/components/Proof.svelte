<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- A rendered page laid on the bench: true page aspect, sheet shadow, crop
     marks when the surrounding link or button is hovered or focused. -->
<script lang="ts">
  let {
    src = '',
    alt,
    letter = false,
    marked = false,
    eager = false,
    blank = 'No page preview stored for this revision',
  }: { src?: string; alt: string; letter?: boolean; marked?: boolean; eager?: boolean; blank?: string } = $props();
  let failed = $state(false);
  $effect(() => {
    void src;
    failed = false;
  });
</script>

<span class="proof" class:letter class:marked>
  {#if src && !failed}
    <img {src} {alt} loading={eager ? 'eager' : 'lazy'} decoding="async" onerror={() => (failed = true)} />
  {:else}
    <span class="blank">{blank}</span>
  {/if}
</span>
