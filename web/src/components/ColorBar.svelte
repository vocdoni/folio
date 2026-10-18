<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- The process-colour strip of a print sheet; it runs while Folio works. -->
<script lang="ts">
  let { busy = false }: { busy?: boolean } = $props();
</script>

<span class="bar" class:busy aria-hidden="true"><i class="c"></i><i class="m"></i><i class="y"></i><i class="k"></i></span>

<style>
  .bar {
    display: inline-flex;
    gap: 2px;
    vertical-align: middle;
  }
  i {
    display: block;
    width: 9px;
    height: 9px;
  }
  .c { background: var(--cyan); }
  .m { background: var(--magenta); }
  .y { background: var(--yellow); }
  .k { background: var(--ink); }
  .busy i {
    animation: pulse 1.2s ease-in-out infinite;
  }
  .busy .m { animation-delay: 0.15s; }
  .busy .y { animation-delay: 0.3s; }
  .busy .k { animation-delay: 0.45s; }
  @keyframes pulse {
    0%, 100% { transform: scaleY(1); }
    50% { transform: scaleY(0.35); }
  }
</style>
