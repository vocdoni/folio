<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts" module>
  // 24 px stroke icons; colour comes from currentColor (no style attributes).
  const paths: Record<string, string> = {
    download: 'M12 4v11m0 0-4.5-4.5M12 15l4.5-4.5M5 19h14',
    upload: 'M12 19V8m0 0-4.5 4.5M12 8l4.5 4.5M5 5h14',
    trash: 'M5 7h14M10 7V5h4v2m-7 0 1 12h8l1-12',
    edit: 'M5 19l1-4L16 5l3 3L9 18l-4 1zM14 7l3 3',
    restore: 'M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
    check: 'M5 12.5 10 17l9-10',
    plus: 'M12 5v14M5 12h14',
    close: 'M6 6l12 12M18 6 6 18',
    left: 'M15 5l-7 7 7 7',
    right: 'M9 5l7 7-7 7',
    copy: 'M9 9h10v10H9zM5 15V5h10',
    search: 'M11 18a7 7 0 1 1 0-14 7 7 0 0 1 0 14zm5-2 4 4',
    menu: 'M4 7h16M4 12h16M4 17h16',
    external: 'M14 5h5v5m0-5-8 8M10 5H5v14h14v-5',
    alert: 'M12 4 3 20h18L12 4zm0 6v4m0 3v.5',
    assistant: 'M5 19l9-9m2-5 .8 2.2L19 8l-2.2.8L16 11l-.8-2.2L13 8l2.2-.8zM7 4l.5 1.5L9 6l-1.5.5L7 8l-.5-1.5L5 6l1.5-.5z',
    show: 'M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12zM12 14.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z',
    hide: 'M3 3l18 18M10 6a9 9 0 0 1 2-.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-3 3.8M6.3 7.3A16 16 0 0 0 2.5 12S6 18.5 12 18.5c1.6 0 3-.4 4.2-1',
    refresh: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4',
  };
</script>

<script lang="ts">
  let { name, label = '' }: { name: keyof typeof paths | string; label?: string } = $props();
</script>

<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
  aria-hidden={label ? undefined : 'true'} role={label ? 'img' : undefined} aria-label={label || undefined} focusable="false">
  <path d={paths[name] ?? ''} />
</svg>
