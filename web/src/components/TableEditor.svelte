<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- One row per item (budget lines, totals, milestones, deliverables,
     signatures). Every value is text, written exactly as it should appear. -->
<script lang="ts">
  import { pointer, type Field } from '../lib/schema';
  import Icon from './Icon.svelte';

  type Obj = Record<string, unknown>;
  let {
    field,
    rows = $bindable(),
    id,
    base,
    inferred = new Set<string>(),
  }: { field: Extract<Field, { kind: 'table' }>; rows: Obj[]; id: string; base: string; inferred?: Set<string> } = $props();

  function add() {
    rows = [...rows, {}];
  }
  function remove(i: number) {
    rows = rows.filter((_, j) => j !== i);
  }
  function cell(r: Obj, c: Field): string {
    const v = r[c.key];
    if (Array.isArray(v)) return v.join(', ');
    return typeof v === 'string' ? v : '';
  }
  function set(i: number, c: Field, s: string) {
    const r = rows[i]!;
    if (c.kind === 'list') r[c.key] = s.split(',').map((x) => x.trim()).filter(Boolean);
    else r[c.key] = s;
  }
</script>

<fieldset class="table-field">
  <legend>{field.label}{#if field.required}<span class="req" aria-hidden="true"> *</span>{/if}</legend>
  {#if field.help}<p class="help small muted">{field.help}</p>{/if}
  {#if rows.length}
    <div class="scroll">
      <table>
        <thead>
          <tr>
            {#each field.columns as c (c.key)}<th scope="col">{c.label}{#if c.required}<span class="req" aria-hidden="true"> *</span>{/if}</th>{/each}
            <th><span class="visually-hidden">Remove</span></th>
          </tr>
        </thead>
        <tbody>
          {#each rows as r, i (i)}
            <tr>
              {#each field.columns as c (c.key)}
                {@const ptr = base + pointer(i, c.key)}
                <td class:inferred={inferred.has(ptr)}>
                  {#if c.kind === 'bool'}
                    <input type="checkbox" aria-label="{c.label}, row {i + 1}" checked={r[c.key] === true}
                      onchange={(e) => (r[c.key] = (e.currentTarget as HTMLInputElement).checked)} />
                  {:else if c.kind === 'choice'}
                    <select aria-label="{c.label}, row {i + 1}" value={cell(r, c)} onchange={(e) => set(i, c, (e.currentTarget as HTMLSelectElement).value)}>
                      <option value="">—</option>
                      {#each c.options as o (o)}<option value={o}>{o}</option>{/each}
                    </select>
                  {:else}
                    <input type="text" aria-label="{c.label}, row {i + 1}" value={cell(r, c)}
                      placeholder={c.kind === 'list' ? 'comma-separated' : ''}
                      oninput={(e) => set(i, c, (e.currentTarget as HTMLInputElement).value)} />
                  {/if}
                </td>
              {/each}
              <td class="rm"><button type="button" class="btn quiet small" onclick={() => remove(i)} aria-label="Remove row {i + 1} of {field.label}"><Icon name="close" /></button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  <button type="button" class="btn small" id="{id}-add" onclick={add}><Icon name="plus" />Add {rows.length ? 'a row' : 'the first row'}</button>
</fieldset>

<style>
  .table-field {
    margin-bottom: var(--s-5);
  }
  .table-field > legend {
    font-weight: 700;
    font-size: var(--fs-1);
  }
  .scroll {
    overflow-x: auto;
    margin-bottom: var(--s-2);
  }
  table {
    border-collapse: collapse;
    min-width: 100%;
  }
  th {
    text-align: left;
    font-size: var(--fs-0);
    font-weight: 600;
    color: var(--ink-2);
    padding: 0 var(--s-1) var(--s-1);
    white-space: nowrap;
  }
  td {
    padding: 2px;
    min-width: 7rem;
  }
  td.rm {
    min-width: 0;
    width: 1%;
  }
  td input[type='text'],
  td select {
    min-height: 2.25rem;
    padding: var(--s-1) var(--s-2);
  }
  td.inferred input {
    background: var(--yellow-tint);
    border-color: var(--yellow);
  }
</style>
