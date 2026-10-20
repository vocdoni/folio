<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- The activity timeline: real sequence, grouped by minute; each row says
     who did what to which item, how it ended and how long it took. -->
<script lang="ts">
  import type { ActivityRow } from '../lib/types';
  import { actionName, clock, errorName, minuteKey, when } from '../lib/format';
  import { href } from '../lib/router.svelte';

  let { rows, fresh = new Set<number>(), compact = false }: { rows: ActivityRow[]; fresh?: Set<number>; compact?: boolean } = $props();

  let groups = $derived.by(() => {
    const out: { key: string; rows: ActivityRow[] }[] = [];
    for (const r of rows) {
      const k = minuteKey(r.ts);
      const last = out[out.length - 1];
      if (last && last.key === k) last.rows.push(r);
      else out.push({ key: k, rows: [r] });
    }
    return out;
  });

  function target(r: ActivityRow): { text: string; link?: string } | null {
    if (!r.target_id) return null;
    const id = r.target_id;
    switch (r.target_kind) {
      case 'document':
        return { text: '…' + id.slice(-6), link: href('/documents/' + encodeURIComponent(id)) }; // ids start with a timestamp: the tail tells them apart
      case 'template':
        return { text: id, link: href('/templates/' + encodeURIComponent(id)) };
      case 'brand':
        return { text: id, link: href('/brands/' + encodeURIComponent(id)) };
      case 'asset':
        return { text: 'image …' + id.slice(-6), link: href('/assets') };
    }
    return { text: id };
  }

  function extra(r: ActivityRow): string {
    const d = r.detail ?? {};
    const parts: string[] = [];
    if (d.revision) parts.push(`revision ${d.revision}`);
    if (d.version) parts.push(`version ${d.version}`);
    if (d.pages) parts.push(d.pages === 1 ? '1 page' : `${d.pages} pages`);
    if (d.formats?.length) parts.push(d.formats.map((f) => f.toUpperCase()).join(' and '));
    if (d.unchanged) parts.push('unchanged');
    if (d.template_id) parts.push('inline template');
    if (d.method === 'oidc') parts.push('with SSO');
    if (d.purge) parts.push('permanently');
    if (d.findings_errors) parts.push(`${d.findings_errors} errors`);
    return parts.join(', ');
  }
</script>

{#if rows.length === 0}
  <p class="empty muted">No activity yet. When an agent calls Folio or you act here, it shows up at once.</p>
{:else}
  <ol class="timeline" class:compact>
    {#each groups as g (g.key)}
      <li class="minute">
        <time class="stamp" datetime={g.key}>{compact ? clock(g.key) : when(g.key)}</time>
        <ul>
          {#each g.rows as r (r.id)}
            {@const t = target(r)}
            <li class="row" class:fresh={fresh.has(r.id)} class:error={r.outcome === 'error'}>
              <span class="mark" title={r.outcome === 'ok' ? 'Succeeded' : 'Failed'}></span>
              <span class="who" title={r.actor}>{r.actor}</span>
              <span class="what">
                {actionName(r.action)}
                {#if t}{#if t.link}<a href={t.link}>{t.text}</a>{:else}<span>{t.text}</span>{/if}{/if}
                {#if !compact && extra(r)}<span class="muted">({extra(r)})</span>{/if}
                {#if r.outcome === 'error'}<span class="failed">failed: {errorName(r.error_code)}</span>{/if}
              </span>
              <span class="dur tabular muted">{r.duration_ms < 1000 ? `${r.duration_ms} ms` : `${(r.duration_ms / 1000).toFixed(1)} s`}</span>
              {#if !compact}<span class="tool muted">{r.source === 'mcp' ? r.action : 'web UI'}</span>{/if}
            </li>
          {/each}
        </ul>
      </li>
    {/each}
  </ol>
{/if}

<style>
  .timeline,
  .timeline ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .minute {
    display: grid;
    grid-template-columns: 7.5rem 1fr;
    gap: var(--s-3);
    padding: var(--s-2) 0;
    border-top: 1px solid var(--rule);
  }
  .compact .minute {
    grid-template-columns: 3.5rem 1fr;
  }
  .stamp {
    font-weight: 700;
    font-size: var(--fs-0);
    padding-top: 0.2rem;
  }
  .row {
    display: grid;
    grid-template-columns: 10px minmax(4rem, 7rem) 1fr auto auto;
    align-items: baseline;
    gap: var(--s-3);
    padding: 0.2rem 0;
  }
  .compact .row {
    grid-template-columns: 10px minmax(3rem, 5rem) 1fr auto;
  }
  .mark {
    width: 10px;
    height: 10px;
    border-radius: 2px;
    background: var(--cyan);
    align-self: center;
  }
  .error .mark {
    background: var(--error);
  }
  .who {
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .what a {
    font-weight: 600;
  }
  .failed {
    color: var(--error);
    font-weight: 600;
  }
  .tool {
    font-size: var(--fs-0);
  }
  .dur {
    font-size: var(--fs-0);
  }
  .fresh {
    animation: arrive 360ms ease-out;
  }
  @keyframes arrive {
    from {
      transform: translateY(-6px);
      background: var(--cyan-tint);
    }
    to {
      transform: none;
      background: transparent;
    }
  }
  .empty {
    padding: var(--s-4) 0;
  }
  @media (max-width: 720px) {
    .minute,
    .compact .minute {
      grid-template-columns: 1fr;
      gap: var(--s-1);
    }
    .row,
    .compact .row {
      grid-template-columns: 10px 1fr auto;
    }
    .who {
      grid-column: 2;
    }
    .what {
      grid-column: 2 / 4;
    }
    .dur {
      grid-column: 3;
      grid-row: 1;
    }
    .tool {
      display: none;
    }
  }
</style>
