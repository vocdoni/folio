<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- The template studio: structure | properties | live proof. Every change
     re-renders the sample 800 ms after the last edit (one preview in flight
     at a time); saving creates a new version. -->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { router } from '../lib/router.svelte';
  import { session } from '../lib/session.svelte';
  import { toasts } from '../lib/toast.svelte';
  import { work } from '../lib/work.svelte';
  import { handoff } from '../lib/handoff';
  import { clock } from '../lib/format';
  import { fromBuilder, metadataFields, objectFields, toBuilder, type BuilderField } from '../lib/schema';
  import type { BrandSummary, Finding, Item, PreviewResult, TemplateSpec } from '../lib/types';
  import SchemaForm from '../components/SchemaForm.svelte';
  import Findings from '../components/Findings.svelte';
  import Proof from '../components/Proof.svelte';
  import Icon from '../components/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';
  import SchemaBuilder from '../components/SchemaBuilder.svelte';

  let { id }: { id: string } = $props();
  const isNew = $derived(!id);
  const today = new Date().toISOString().slice(0, 10);

  function blank(brand: string): TemplateSpec {
    return {
      id: '', name: '', description: '', category: '', brand_id: brand, languages: ['en'],
      layout: { base: 'report', typst: null, options: { cover: true, toc: false, page_size: 'a4', heading_numbering: false } },
      sections: [{ title: 'Summary', required: true }], components: [], strict: false,
      body_skeleton: '# Summary\n\nTODO\n',
      sample: { markdown: '# Summary\n\nA short example that shows how the layout looks.\n', metadata: { title: 'Sample document', date: today, language: 'en' } },
    };
  }

  let spec = $state<TemplateSpec | null>(null);
  let latest = $state(0);
  let archived = $state(false);
  let brands = $state<BrandSummary[]>([]);
  let loadError = $state('');
  let drafted = $state<{ findings: Finding[]; repaired: boolean } | null>(null);
  let pane = $state<'basics' | 'layout' | 'sections' | 'fields' | 'data' | 'outline' | 'sample' | 'labels' | 'advanced'>('basics');
  let mobileTab = $state<'structure' | 'properties' | 'proof'>('properties');
  let dirty = $state(false);
  let note = $state('');
  let saving = $state(false);
  let saveFindings = $state<Finding[]>([]);
  let saveError = $state('');
  let conflict = $state(false);
  let confirmArchive = $state(false);

  // Live proof state.
  let proof = $state<PreviewResult | null>(null);
  let proofAt = $state<Date | null>(null);
  let proofBusy = $state(false);
  let proofError = $state('');
  let proofPage = $state(1);

  onMount(async () => {
    try {
      brands = (await api.get<{ items: BrandSummary[] }>('/brands')).items;
      if (isNew) {
        const d = handoff.draft;
        handoff.draft = null;
        spec = d ? d.spec : blank(brands[0]?.id ?? '');
        if (d) {
          drafted = { findings: d.findings, repaired: d.repaired };
          dirty = true;
        }
      } else {
        const it = await api.get<Item<TemplateSpec>>(`/templates/${encodeURIComponent(id)}`);
        spec = it.spec;
        latest = it.latest_version;
        archived = it.archived;
      }
    } catch (e) {
      loadError = e instanceof ApiError && e.status === 404 ? `There is no template “${id}”.` : e instanceof Error ? e.message : String(e);
    }
  });

  // ---- live preview: debounce 800 ms, one request in flight, latest spec wins ----
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inflight = false;
  let pending = false;
  let first = true;
  $effect(() => {
    if (!spec) return;
    JSON.stringify(spec); // track every change
    untrack(() => {
      if (!first) dirty = true;
      first = false;
      clearTimeout(timer);
      timer = setTimeout(sendPreview, proof ? 800 : 50);
    });
  });
  async function sendPreview() {
    if (!spec) return;
    if (inflight) {
      pending = true;
      return;
    }
    inflight = true;
    proofBusy = true;
    proofError = '';
    try {
      const res = await work.run(api.post<PreviewResult>('/templates/preview', { spec: $state.snapshot(spec), pages: 4 }));
      proof = res;
      proofAt = new Date();
      proofPage = Math.min(proofPage, Math.max(1, res.page_urls.length));
    } catch (e) {
      if (e instanceof ApiError && e.code === 'preview_in_progress') pending = true;
      else proofError = e instanceof Error ? e.message : String(e);
    } finally {
      inflight = false;
      proofBusy = false;
      if (pending) {
        pending = false;
        setTimeout(sendPreview, 200);
      }
    }
  }

  async function save() {
    if (!spec) return;
    saving = true;
    saveError = '';
    saveFindings = [];
    conflict = false;
    try {
      const body = { template: $state.snapshot(spec), note, expected_version: latest };
      const res = isNew
        ? await work.run(api.post<{ id: string; version: number }>('/templates', body))
        : await work.run(api.put<{ id: string; version: number }>(`/templates/${encodeURIComponent(id)}`, body));
      toasts.show(`Saved version ${res.version} of ${spec.name || res.id}.`);
      dirty = false;
      note = '';
      latest = res.version;
      if (isNew) router.navigate('/templates/' + res.id, { replace: true });
    } catch (e) {
      if (e instanceof ApiError) {
        conflict = e.code === 'conflict' && !isNew;
        saveError = conflict ? 'Someone saved a newer version while you were editing. Reload it and apply your change again.' : e.message;
        saveFindings = e.details;
      } else saveError = String(e);
    } finally {
      saving = false;
    }
  }
  async function archive() {
    try {
      await api.post(`/templates/${encodeURIComponent(id)}/archive`, { archived: !archived });
      archived = !archived;
      toasts.show(archived ? 'Archived. Agents can no longer render new documents with it.' : 'Restored. Agents can use it again.');
    } catch (e) {
      toasts.show(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      confirmArchive = false;
    }
  }

  // ---- helpers for the property panes ----
  const caps = $derived(session.caps);
  const ctx = $derived({ components: Object.fromEntries((caps?.components ?? []).map((c) => [c.name, c.schema])) });
  const componentNames = $derived((caps?.components ?? []).map((c) => c.name));
  let metaBuilder = $state<BuilderField[] | null>(null);
  let dataBuilder = $state<BuilderField[] | null>(null);
  $effect(() => {
    if (spec && metaBuilder === null && pane === 'fields') metaBuilder = toBuilder(spec.metadata_schema);
    if (spec && dataBuilder === null && pane === 'data') dataBuilder = toBuilder(spec.data_schema);
  });
  function setMeta(b: BuilderField[]) {
    metaBuilder = b;
    spec!.metadata_schema = fromBuilder(b);
  }
  function setData(b: BuilderField[]) {
    dataBuilder = b;
    spec!.data_schema = fromBuilder(b);
    const used = b.filter((f) => f.type.startsWith('component:')).map((f) => f.type.slice('component:'.length));
    for (const c of used) if (!spec!.components.includes(c)) spec!.components.push(c);
  }
  let sampleMeta = $derived(spec && caps ? metadataFields(caps.core_metadata_schema, spec.metadata_schema, spec.languages, ctx) : []);
  let sampleData = $derived(spec ? (objectFields(spec.data_schema, ctx) ?? []) : []);
  function moveSection(i: number, d: number) {
    const s = spec!.sections;
    const j = i + d;
    if (j < 0 || j >= s.length) return;
    [s[i], s[j]] = [s[j]!, s[i]!];
  }
  let rawText = $state('');
  let rawError = $state('');
  $effect(() => {
    if (pane === 'advanced' && spec) rawText = untrack(() => JSON.stringify($state.snapshot(spec), null, 2));
  });
  function applyRaw() {
    try {
      spec = JSON.parse(rawText) as TemplateSpec;
      rawError = '';
      metaBuilder = dataBuilder = null;
      toasts.show('Applied the JSON. The proof is updating.');
    } catch (e) {
      rawError = 'This is not valid JSON: ' + (e instanceof Error ? e.message : String(e));
    }
  }
  function toggle<T>(list: T[], v: T, on: boolean) {
    const i = list.indexOf(v);
    if (on && i < 0) list.push(v);
    if (!on && i >= 0) list.splice(i, 1);
  }
  const panes = [
    ['basics', 'Name and brand'],
    ['layout', 'Layout'],
    ['sections', 'Sections'],
    ['fields', 'Document details'],
    ['data', 'Tables and figures'],
    ['outline', 'Starting outline'],
    ['sample', 'Sample document'],
    ['labels', 'Labels'],
    ['advanced', 'JSON and Typst'],
  ] as const;
  let labelLang = $state('');
  $effect(() => {
    if (spec && !labelLang) labelLang = spec.languages[0] ?? 'en';
  });
  function labelValue(k: string): string {
    return spec?.labels?.[labelLang]?.[k] ?? '';
  }
  function setLabel(k: string, v: string) {
    if (!spec) return;
    spec.labels ??= {};
    spec.labels[labelLang] ??= {};
    if (v) spec.labels[labelLang]![k] = v;
    else delete spec.labels[labelLang]![k];
  }
  addEventListener('beforeunload', (e) => {
    if (dirty) e.preventDefault();
  });
</script>

{#if loadError}
  <div class="page"><p class="note error">{loadError}</p></div>
{:else if spec}
  <div class="studio">
    <div class="bar">
      <div class="title">
        <h1>{spec.name || (isNew ? 'New template' : id)}</h1>
        <p class="small muted">
          {#if isNew}Not saved yet{:else}Version {latest}{#if archived}, archived{/if}{/if}{#if dirty}, unsaved changes{/if}
        </p>
      </div>
      <div class="save">
        <label class="visually-hidden" for="note">What changed</label>
        <input id="note" type="text" placeholder="What changed (optional)" bind:value={note} />
        {#if !isNew}<button class="btn" onclick={() => (confirmArchive = true)}>{archived ? 'Restore' : 'Archive'}</button>{/if}
        <button class="btn primary" onclick={save} disabled={saving || (!dirty && !isNew)}>{saving ? 'Saving…' : isNew ? 'Create template' : 'Save as new version'}</button>
      </div>
    </div>
    {#if saveError}
      <div class="note error msg" role="alert">
        <p>{saveError}</p>
        {#if conflict}<button class="btn small" onclick={() => location.reload()}>Reload the latest version</button>{/if}
      </div>
    {/if}
    {#if saveFindings.length}<div class="msg"><Findings findings={saveFindings} title="Fix these before saving" /></div>{/if}
    {#if drafted}
      <div class="note warn msg">
        <p><strong>Drafted by the assistant{drafted.repaired ? ' (after one correction)' : ''}.</strong> Check every section and the sample, then save. Nothing is stored until you do.</p>
      </div>
    {/if}

    <div class="tabs" role="tablist" aria-label="Studio panes">
      {#each [['structure', 'Structure'], ['properties', 'Properties'], ['proof', 'Proof']] as [k, l] (k)}
        <button role="tab" aria-selected={mobileTab === k} class:on={mobileTab === k} onclick={() => (mobileTab = k as typeof mobileTab)}>{l}</button>
      {/each}
    </div>

    <div class="bench3">
      <nav class="structure" class:show={mobileTab === 'structure'} aria-label="Template structure">
        <ol>
          {#each panes as [k, l] (k)}
            <li><button class:on={pane === k} aria-current={pane === k ? 'true' : undefined} onclick={() => { pane = k; mobileTab = 'properties'; }}>{l}</button></li>
          {/each}
        </ol>
        <div class="outline">
          <p class="small muted">Sections</p>
          <ol>{#each spec.sections as s, i (i)}<li class="small">{s.title || 'Untitled'}{#if s.component} <span class="comp">{s.component}</span>{/if}</li>{/each}</ol>
        </div>
      </nav>

      <section class="properties sheet" class:show={mobileTab === 'properties'} aria-label="Properties">
        {#if pane === 'basics'}
          <h2 class="sheet-title">Name and brand</h2>
          <div class="field">
            <label for="t-id">Template id</label>
            <input id="t-id" type="text" bind:value={spec.id} disabled={!isNew} pattern="[a-z0-9][a-z0-9-]+" aria-describedby="t-id-help" />
            <span class="help" id="t-id-help">{isNew ? 'Lowercase letters, digits and hyphens. Agents refer to the template by it; it cannot change later.' : 'The id cannot change.'}</span>
          </div>
          <div class="field"><label for="t-name">Name</label><input id="t-name" type="text" bind:value={spec.name} /></div>
          <div class="field"><label for="t-desc">Description</label><textarea id="t-desc" rows="3" bind:value={spec.description}></textarea>
            <span class="help">Agents read this to choose a template; say what documents it is for.</span></div>
          <div class="field"><label for="t-cat">Category</label><input id="t-cat" type="text" bind:value={spec.category} placeholder="proposal, quote, report…" /></div>
          <div class="field">
            <label for="t-brand">Brand</label>
            <select id="t-brand" bind:value={spec.brand_id}>{#each brands as b (b.id)}<option value={b.id}>{b.spec.name}</option>{/each}</select>
          </div>
          <fieldset>
            <legend>Languages</legend>
            {#each caps?.languages ?? [] as l (l)}
              <label class="check"><input type="checkbox" checked={spec.languages.includes(l)} onchange={(e) => toggle(spec!.languages, l, (e.currentTarget as HTMLInputElement).checked)} />{l}</label>
            {/each}
            <p class="help small muted">The first language is the default for new documents.</p>
          </fieldset>
        {:else if pane === 'layout'}
          <h2 class="sheet-title">Layout</h2>
          <fieldset>
            <legend>Base layout</legend>
            <div class="choices">
              {#each caps?.layouts ?? [] as l (l.name)}
                <label class="choice"><input type="radio" name="base" value={l.name} bind:group={spec.layout.base} /><span>{l.name}</span></label>
              {/each}
            </div>
          </fieldset>
          {#if spec.layout.options}
            <label class="check"><input type="checkbox" bind:checked={spec.layout.options.cover} />Cover page</label>
            <label class="check"><input type="checkbox" bind:checked={spec.layout.options.toc} />Table of contents</label>
            <label class="check"><input type="checkbox" bind:checked={spec.layout.options.heading_numbering} />Numbered headings</label>
            <div class="field narrow"><label for="t-size">Page size</label>
              <select id="t-size" bind:value={spec.layout.options.page_size}><option value="a4">A4</option><option value="letter">US Letter</option></select></div>
          {/if}
          {#if spec.layout.typst}<p class="note small">This template uses a custom Typst layout (see JSON and Typst); the base layout above is only its starting point.</p>{/if}
        {:else if pane === 'sections'}
          <h2 class="sheet-title">Sections</h2>
          <p class="help small muted">The headings a document must or may have, in order. A section can hold a table such as the budget.</p>
          <label class="check"><input type="checkbox" bind:checked={spec.strict} />Only these sections are allowed</label>
          <ol class="sections">
            {#each spec.sections as s, i (i)}
              <li class="sec">
                <div class="sec-head">
                  <span class="num">{i + 1}</span>
                  <input type="text" aria-label="Title of section {i + 1}" bind:value={s.title} />
                  <button class="btn quiet small" onclick={() => moveSection(i, -1)} disabled={i === 0} aria-label="Move section {i + 1} up">Up</button>
                  <button class="btn quiet small" onclick={() => moveSection(i, 1)} disabled={i === spec.sections.length - 1} aria-label="Move section {i + 1} down">Down</button>
                  <button class="btn quiet small" onclick={() => spec!.sections.splice(i, 1)} aria-label="Remove section {i + 1}"><Icon name="close" /></button>
                </div>
                <div class="sec-body">
                  <label class="check small"><input type="checkbox" bind:checked={s.required} />Required</label>
                  <label class="small">Table
                    <select bind:value={s.component} aria-label="Table in section {i + 1}">
                      <option value={undefined}>None</option>
                      {#each spec.components as c (c)}<option value={c}>{c}</option>{/each}
                    </select>
                  </label>
                  <input type="text" class="small" placeholder="Other headings that count (comma-separated)" aria-label="Aliases of section {i + 1}"
                    value={(s.aliases ?? []).join(', ')} onchange={(e) => (s.aliases = (e.currentTarget as HTMLInputElement).value.split(',').map((x) => x.trim()).filter(Boolean))} />
                  <input type="text" class="small" placeholder="Hint for writers" aria-label="Hint for section {i + 1}" bind:value={s.hint} />
                </div>
              </li>
            {/each}
          </ol>
          <button class="btn small" onclick={() => spec!.sections.push({ title: '', required: false })}><Icon name="plus" />Add a section</button>
          <fieldset class="comps">
            <legend>Tables this template can use</legend>
            {#each componentNames as c (c)}
              <label class="check"><input type="checkbox" checked={spec.components.includes(c)} onchange={(e) => toggle(spec!.components, c, (e.currentTarget as HTMLInputElement).checked)} />{c}</label>
            {/each}
          </fieldset>
        {:else if pane === 'fields'}
          <h2 class="sheet-title">Document details</h2>
          <p class="help small muted">Title, date, language, author, client, project, reference, version label and confidentiality always exist. Add the details this kind of document also needs.</p>
          {#if metaBuilder}
            <SchemaBuilder fields={metaBuilder} onchange={setMeta} components={[]} />
          {:else}
            <p class="note small">This schema uses JSON Schema features the builder does not show. Edit it in JSON and Typst.</p>
          {/if}
        {:else if pane === 'data'}
          <h2 class="sheet-title">Tables and figures</h2>
          <p class="help small muted">Structured data the document carries, usually built-in tables. Every figure is text written as it should appear.</p>
          {#if dataBuilder}
            <SchemaBuilder fields={dataBuilder} onchange={setData} components={componentNames.filter((c) => c !== 'info')} />
          {:else}
            <p class="note small">This schema uses JSON Schema features the builder does not show. Edit it in JSON and Typst.</p>
          {/if}
        {:else if pane === 'outline'}
          <h2 class="sheet-title">Starting outline</h2>
          <p class="help small muted">The Markdown a writer or an agent starts from: headings, TODO placeholders and table blocks such as <code>::: folio-budget</code>.</p>
          <textarea class="code tall" aria-label="Starting outline" bind:value={spec.body_skeleton}></textarea>
        {:else if pane === 'sample'}
          <h2 class="sheet-title">Sample document</h2>
          <p class="help small muted">The proof renders this sample. Make it realistic: it shows how real documents will look.</p>
          <div class="field"><label for="s-md">Sample text (Markdown)</label><textarea id="s-md" class="code tall" bind:value={spec.sample.markdown}></textarea></div>
          <h3 class="sub">Sample details</h3>
          <SchemaForm fields={sampleMeta} bind:value={spec.sample.metadata} idPrefix="sample-meta" base="/sample/metadata" />
          {#if sampleData.length}
            <h3 class="sub">Sample tables</h3>
            <SchemaForm fields={sampleData} bind:value={() => (spec!.sample.data ??= {}), (v) => (spec!.sample.data = v)} idPrefix="sample-data" base="/sample/data" />
          {/if}
        {:else if pane === 'labels'}
          <h2 class="sheet-title">Labels</h2>
          <p class="help small muted">Override the words Folio prints around your content, such as table headings, per language. Empty uses Folio’s built-in wording.</p>
          <div class="field narrow"><label for="l-lang">Language</label>
            <select id="l-lang" bind:value={labelLang}>{#each spec.languages as l (l)}<option value={l}>{l}</option>{/each}</select></div>
          <table class="ledger labels">
            <thead><tr><th scope="col">Label</th><th scope="col">Your wording</th></tr></thead>
            <tbody>
              {#each caps?.label_keys ?? [] as k (k)}
                <tr><td class="code">{k}</td><td><input type="text" aria-label="Wording for {k}" value={labelValue(k)} onchange={(e) => setLabel(k, (e.currentTarget as HTMLInputElement).value.trim())} /></td></tr>
              {/each}
            </tbody>
          </table>
        {:else if pane === 'advanced'}
          <h2 class="sheet-title">JSON and Typst</h2>
          <div class="field">
            <label for="raw">The whole template as JSON</label>
            <textarea id="raw" class="code tall" bind:value={rawText} spellcheck="false" aria-invalid={rawError ? 'true' : undefined}></textarea>
            {#if rawError}<span class="error-text" role="alert">{rawError}</span>{/if}
          </div>
          <button class="btn" onclick={applyRaw}>Apply the JSON</button>
          <div class="field typst">
            <label for="typst">Custom Typst layout (experts)</label>
            <textarea id="typst" class="code tall" spellcheck="false" value={spec.layout.typst ?? ''}
              onchange={(e) => (spec!.layout.typst = (e.currentTarget as HTMLTextAreaElement).value.trim() || null)}></textarea>
            <span class="help">Leave empty to use the base layout. A custom layout must define <code>folio-layout(meta:, brand:, labels:, options:, body)</code>.</span>
          </div>
        {/if}
      </section>

      <section class="proof-pane" class:show={mobileTab === 'proof'} aria-label="Live proof" aria-busy={proofBusy}>
        <p class="status small" role="status">
          {#if proofBusy}<span class="dot busy"></span>Updating the proof…
          {:else if proofError}<span class="dot bad"></span>{proofError}
          {:else if proof && !proof.ok}<span class="dot bad"></span>The sample does not render yet
          {:else if proofAt}<span class="dot ok"></span><span>Proof updated at <time class="num">{clock(proofAt)}</time>, {proof?.pages} {proof?.pages === 1 ? 'page' : 'pages'}</span>
          {:else}Preparing the proof…{/if}
        </p>
        {#if proof && !proof.ok}
          <Findings findings={proof.findings} title={proof.message ?? 'Problems'} />
        {/if}
        {#if proof?.ok}
          <div class="sheetstack">
            <Proof src={proof.page_urls[proofPage - 1] ?? ''} alt="Sample page {proofPage}" eager marked letter={spec.layout.options?.page_size === 'letter'} />
          </div>
          {#if proof.page_urls.length > 1}
            <div class="pager">
              <button class="btn small" onclick={() => proofPage--} disabled={proofPage <= 1} aria-label="Previous page"><Icon name="left" /></button>
              <span class="num small">Page {proofPage} of {proof.page_urls.length}{proof.pages > proof.page_urls.length ? ` (${proof.pages} in total)` : ''}</span>
              <button class="btn small" onclick={() => proofPage++} disabled={proofPage >= proof.page_urls.length} aria-label="Next page"><Icon name="right" /></button>
            </div>
          {/if}
          {#if proof.findings.length}<Findings findings={proof.findings} title="Placeholders left in the sample" />{/if}
        {/if}
        {#if drafted?.findings.length}<Findings findings={drafted.findings} title="What the assistant could not fix" />{/if}
      </section>
    </div>
  </div>
  <Dialog bind:open={confirmArchive} title={archived ? 'Restore this template?' : 'Archive this template?'} confirm={archived ? 'Restore' : 'Archive'} onconfirm={archive}>
    <p>{archived ? 'Agents can render new documents with it again.' : 'Agents can no longer render new documents with it. Existing documents keep their files, and you can restore it later.'}</p>
  </Dialog>
{/if}

<style>
  .studio {
    padding: var(--s-4) clamp(1rem, 2vw, 1.75rem) var(--s-6);
  }
  .bar {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: var(--s-3) var(--s-5);
    margin-bottom: var(--s-4);
  }
  .bar h1 {
    font-size: var(--fs-4);
  }
  .save {
    display: flex;
    gap: var(--s-2);
    align-items: center;
  }
  .save input {
    width: 18rem;
  }
  .msg {
    margin-bottom: var(--s-4);
  }
  .bench3 {
    display: grid;
    grid-template-columns: 13rem minmax(0, 1fr) minmax(0, 1.05fr);
    gap: var(--s-5);
    align-items: start;
  }
  .structure ol {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 2px;
  }
  .structure button {
    width: 100%;
    text-align: left;
    padding: var(--s-2) var(--s-3);
    border: 0;
    border-left: 3px solid transparent;
    background: none;
    font: inherit;
    font-weight: 600;
    color: var(--ink-2);
    cursor: pointer;
  }
  .structure button:hover {
    color: var(--ink);
    background: var(--table-2);
  }
  .structure button.on {
    color: var(--ink);
    border-left-color: var(--magenta);
    background: var(--paper);
  }
  .outline {
    margin-top: var(--s-5);
    padding-top: var(--s-3);
    border-top: 1px solid var(--rule);
  }
  .outline ol {
    list-style: decimal;
    padding-left: 1.3rem;
    color: var(--ink-2);
  }
  .comp {
    margin-left: 0.4em;
    font-size: 0.7rem;
    font-weight: 700;
    color: var(--magenta-deep);
  }
  .properties {
    max-height: calc(100vh - var(--masthead-h) - 9rem);
    overflow-y: auto;
  }
  .narrow {
    max-width: 14rem;
  }
  .choices {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(7rem, 1fr));
    gap: var(--s-2);
  }
  .choice {
    display: flex;
    align-items: center;
    gap: var(--s-2);
    padding: var(--s-2) var(--s-3);
    border: 1px solid var(--rule-strong);
    border-radius: var(--radius-control);
    font-weight: 600;
    cursor: pointer;
  }
  .choice:has(input:checked) {
    border-color: var(--magenta);
    background: var(--magenta-tint);
  }
  .check {
    display: flex;
    margin-bottom: var(--s-2);
  }
  .sections {
    list-style: none;
    margin: var(--s-3) 0;
    padding: 0;
    display: grid;
    gap: var(--s-3);
  }
  .sec {
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    padding: var(--s-2);
  }
  .sec-head {
    display: flex;
    gap: var(--s-1);
    align-items: center;
  }
  .sec-head .num {
    width: 1.5rem;
    text-align: center;
    font-weight: 700;
    color: var(--ink-2);
  }
  .sec-body {
    display: grid;
    grid-template-columns: auto auto;
    gap: var(--s-2) var(--s-3);
    padding: var(--s-2) 0 0 1.75rem;
    align-items: center;
  }
  .sec-body input[type='text'] {
    grid-column: 1 / -1;
    min-height: 2.1rem;
  }
  .sec-body select {
    min-height: 2rem;
    width: auto;
    margin-left: var(--s-2);
  }
  .comps {
    margin-top: var(--s-5);
  }
  .tall {
    min-height: 22rem;
  }
  .sub {
    font-size: var(--fs-1);
    margin: var(--s-4) 0 var(--s-3);
  }
  .labels td {
    padding: var(--s-1) var(--s-2);
  }
  .typst {
    margin-top: var(--s-5);
  }
  .proof-pane {
    position: sticky;
    top: calc(var(--masthead-h) + var(--s-3));
    display: grid;
    gap: var(--s-4);
    justify-items: center;
  }
  .status {
    justify-self: stretch;
    display: flex;
    align-items: center;
    gap: var(--s-2);
    margin: 0;
    color: var(--ink-2);
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--rule-strong);
  }
  .dot.ok {
    background: var(--cyan);
  }
  .dot.bad {
    background: var(--error);
  }
  .dot.busy {
    background: var(--yellow);
    animation: blink 1s ease-in-out infinite;
  }
  @keyframes blink {
    50% {
      opacity: 0.3;
    }
  }
  .sheetstack {
    width: min(100%, 30rem);
    padding: var(--s-4);
  }
  .pager {
    display: flex;
    align-items: center;
    gap: var(--s-3);
  }
  .tabs {
    display: none;
  }
  @media (max-width: 1100px) {
    .bench3 {
      grid-template-columns: 1fr;
    }
    .tabs {
      display: flex;
      gap: var(--s-1);
      margin-bottom: var(--s-4);
      border-bottom: 1px solid var(--rule);
    }
    .tabs button {
      flex: 1;
      padding: var(--s-2) var(--s-3);
      background: none;
      border: 0;
      border-bottom: 3px solid transparent;
      font: inherit;
      font-weight: 600;
      color: var(--ink-2);
      cursor: pointer;
    }
    .tabs button.on {
      color: var(--ink);
      border-bottom-color: var(--magenta);
    }
    .structure,
    .properties,
    .proof-pane {
      display: none;
    }
    .structure.show,
    .proof-pane.show {
      display: grid;
    }
    .properties.show {
      display: block;
      max-height: none;
    }
    .proof-pane {
      position: static;
    }
    .save {
      flex-wrap: wrap;
    }
    .save input {
      width: 100%;
    }
  }
</style>
