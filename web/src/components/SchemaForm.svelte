<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Renders form fields generated from a JSON Schema (see lib/schema.ts).
     Values not found verbatim in the user's text (assistant proposals) are
     marked "check this". -->
<script lang="ts">
  import Self from './SchemaForm.svelte';
  import TableEditor from './TableEditor.svelte';
  import JsonField from './JsonField.svelte';
  import { pointer, type Field } from '../lib/schema';

  type Obj = Record<string, unknown>;
  let {
    fields,
    value = $bindable(),
    idPrefix,
    base = '',
    inferred = new Set<string>(),
    errors = {},
  }: { fields: Field[]; value: Obj; idPrefix: string; base?: string; inferred?: Set<string>; errors?: Record<string, string> } = $props();

  function lines(v: unknown): string {
    return Array.isArray(v) ? v.join('\n') : '';
  }
  function setLines(key: string, s: string) {
    const a = s.split('\n').map((x) => x.trim()).filter(Boolean);
    value[key] = a.length ? a : undefined;
  }
  function obj(key: string): Obj {
    const v = value[key];
    if (v && typeof v === 'object' && !Array.isArray(v)) return v as Obj;
    const o: Obj = {};
    value[key] = o;
    return value[key] as Obj;
  }
  function rows(key: string): Obj[] {
    const v = value[key];
    if (Array.isArray(v)) return v as Obj[];
    value[key] = [];
    return value[key] as Obj[];
  }
</script>

{#each fields as f (f.key)}
  {@const id = `${idPrefix}-${f.key}`}
  {@const ptr = base + pointer(f.key)}
  {@const flagged = [...inferred].some((p) => p === ptr || p.startsWith(ptr + '/'))}
  {@const err = errors[ptr]}
  {#if f.kind === 'group'}
    <fieldset class="group" class:component={!!f.component}>
      <legend>{f.label}{#if f.required}<span class="req" aria-hidden="true"> *</span>{/if}</legend>
      {#if f.help}<p class="help">{f.help}</p>{/if}
      <Self fields={f.fields} bind:value={() => obj(f.key), (v) => (value[f.key] = v)} idPrefix={id} base={ptr} {inferred} {errors} />
    </fieldset>
  {:else if f.kind === 'table'}
    <TableEditor field={f} bind:rows={() => rows(f.key), (v) => (value[f.key] = v)} {id} base={ptr} {inferred} />
  {:else}
    <div class="field" class:inferred={flagged}>
      {#if f.kind === 'bool'}
        <label class="check"><input {id} type="checkbox" checked={value[f.key] === true}
          onchange={(e) => (value[f.key] = (e.currentTarget as HTMLInputElement).checked)} />{f.label}</label>
      {:else}
        <label for={id}>{f.label}{#if f.required}<span class="req" aria-hidden="true"> *</span><span class="visually-hidden"> (required)</span>{/if}</label>
        {#if f.kind === 'text' && f.date}
          <input {id} type="date" value={(value[f.key] as string) ?? ''} oninput={(e) => (value[f.key] = (e.currentTarget as HTMLInputElement).value || undefined)}
            aria-invalid={err ? 'true' : undefined} />
        {:else if f.kind === 'text' && f.multiline}
          <textarea {id} rows="3" maxlength={f.maxLength} value={(value[f.key] as string) ?? ''}
            oninput={(e) => (value[f.key] = (e.currentTarget as HTMLTextAreaElement).value)} aria-invalid={err ? 'true' : undefined}></textarea>
        {:else if f.kind === 'text'}
          <input {id} type="text" maxlength={f.maxLength} value={(value[f.key] as string) ?? ''}
            oninput={(e) => (value[f.key] = (e.currentTarget as HTMLInputElement).value)} aria-invalid={err ? 'true' : undefined} />
        {:else if f.kind === 'choice'}
          <select {id} value={(value[f.key] as string) ?? ''} onchange={(e) => (value[f.key] = (e.currentTarget as HTMLSelectElement).value || undefined)}>
            {#if !f.required || !value[f.key]}<option value="">Choose…</option>{/if}
            {#each f.options as o (o)}<option value={o}>{o}</option>{/each}
          </select>
        {:else if f.kind === 'list'}
          <textarea {id} rows="3" value={lines(value[f.key])} onchange={(e) => setLines(f.key, (e.currentTarget as HTMLTextAreaElement).value)}
            aria-describedby="{id}-lines"></textarea>
          <span class="help" id="{id}-lines">One item per line.</span>
        {:else if f.kind === 'json'}
          <JsonField {id} bind:value={() => value[f.key], (v) => (value[f.key] = v)} reason={f.reason} />
        {/if}
      {/if}
      {#if f.help}<span class="help">{f.help}</span>{/if}
      {#if flagged}<span class="flag">Not found word for word in your text. Check it before you render.</span>{/if}
      {#if err}<span class="error-text">{err}</span>{/if}
    </div>
  {/if}
{/each}

<style>
  .group {
    border-top: 1px solid var(--rule);
    padding-top: var(--s-3);
  }
  .group > legend {
    font-size: var(--fs-1);
    font-weight: 700;
  }
  .group > .help {
    margin: 0 0 var(--s-3);
  }
  .inferred input,
  .inferred textarea,
  .inferred select {
    background: var(--yellow-tint);
    border-color: var(--yellow);
  }
  .flag {
    font-size: var(--fs-0);
    font-weight: 600;
  }
  .flag::before {
    content: '';
    display: inline-block;
    width: 8px;
    height: 8px;
    margin-right: 0.4em;
    background: var(--yellow);
  }
</style>
