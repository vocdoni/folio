<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Builds a template's metadata or data schema from a list of fields
     (only the form generator's subset: text, dates, choices, yes/no, lists
     and built-in tables). -->
<script lang="ts">
  import type { BuilderField, BuilderType } from '../lib/schema';
  import Icon from './Icon.svelte';

  let { fields, onchange, components }: { fields: BuilderField[]; onchange: (f: BuilderField[]) => void; components: string[] } = $props();
  const reserved = ['title', 'date', 'language', 'author', 'client', 'project', 'reference', 'version_label', 'confidentiality'];
  const types: [BuilderType, string][] = [
    ['text', 'Short text'],
    ['long-text', 'Long text'],
    ['date', 'Date'],
    ['choice', 'Choice'],
    ['yes-no', 'Yes or no'],
    ['list', 'List of texts'],
  ];

  function update(i: number, patch: Partial<BuilderField>) {
    onchange(fields.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  }
  function add() {
    onchange([...fields, { name: '', label: '', type: components.length ? `component:${components[0]}` : 'text', required: false, options: [] }]);
  }
  function problem(f: BuilderField, i: number): string {
    if (!/^[a-z][a-z0-9_]*$/.test(f.name)) return 'Use lowercase letters, digits and underscores, starting with a letter.';
    if (components.length === 0 && reserved.includes(f.name)) return `“${f.name}” already exists for every document.`;
    if (fields.findIndex((g) => g.name === f.name) !== i) return 'Another field has this name.';
    if (f.type === 'choice' && f.options.filter((o) => o.trim()).length === 0) return 'Add at least one choice.';
    return '';
  }
</script>

{#if fields.length === 0}
  <p class="muted small">No fields yet.</p>
{/if}
<ol class="fields">
  {#each fields as f, i (i)}
    {@const p = problem(f, i)}
    <li class="row">
      <div class="grid">
        <label class="small">Field name
          <input type="text" value={f.name} oninput={(e) => update(i, { name: (e.currentTarget as HTMLInputElement).value })} aria-invalid={p ? 'true' : undefined} />
        </label>
        <label class="small">Label shown in forms
          <input type="text" value={f.label} oninput={(e) => update(i, { label: (e.currentTarget as HTMLInputElement).value })} />
        </label>
        <label class="small">Kind
          <select value={f.type} onchange={(e) => update(i, { type: (e.currentTarget as HTMLSelectElement).value as BuilderType })}>
            {#each types as [v, l] (v)}<option value={v}>{l}</option>{/each}
            {#each components as c (c)}<option value={`component:${c}`}>Table: {c}</option>{/each}
          </select>
        </label>
        <label class="check small"><input type="checkbox" checked={f.required} onchange={(e) => update(i, { required: (e.currentTarget as HTMLInputElement).checked })} />Required</label>
        <button class="btn quiet small rm" onclick={() => onchange(fields.filter((_, j) => j !== i))} aria-label="Remove field {f.name || i + 1}"><Icon name="close" /></button>
      </div>
      {#if f.type === 'choice'}
        <label class="small">Choices, one per line
          <textarea rows="3" value={f.options.join('\n')} oninput={(e) => update(i, { options: (e.currentTarget as HTMLTextAreaElement).value.split('\n') })}></textarea>
        </label>
      {/if}
      {#if p}<span class="error-text small">{p}</span>{/if}
    </li>
  {/each}
</ol>
<button class="btn small" onclick={add}><Icon name="plus" />Add a field</button>

<style>
  .fields {
    list-style: none;
    margin: 0 0 var(--s-3);
    padding: 0;
    display: grid;
    gap: var(--s-3);
  }
  .row {
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    padding: var(--s-3);
    display: grid;
    gap: var(--s-2);
  }
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr auto auto;
    gap: var(--s-2);
    align-items: end;
  }
  label.small {
    display: grid;
    gap: 2px;
    font-weight: 600;
  }
  .rm {
    align-self: end;
  }
  @media (max-width: 700px) {
    .grid {
      grid-template-columns: 1fr 1fr;
    }
  }
</style>
