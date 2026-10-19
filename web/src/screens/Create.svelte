<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Markdown in, document out: pick a template, fill the details, render on
     demand (no live render here), read the proof and download it. -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, ApiError, uuid } from '../lib/api';
  import { router, href } from '../lib/router.svelte';
  import { session } from '../lib/session.svelte';
  import { work } from '../lib/work.svelte';
  import { toasts } from '../lib/toast.svelte';
  import { runJob } from '../lib/jobs';
  import { bytes } from '../lib/format';
  import { metadataFields, objectFields, prune } from '../lib/schema';
  import type { Finding, Item, Plan, RenderResult, Source, TemplateSpec, TemplateSummary, DocumentDetail } from '../lib/types';
  import SchemaForm from '../components/SchemaForm.svelte';
  import Findings from '../components/Findings.svelte';
  import Proof from '../components/Proof.svelte';
  import Icon from '../components/Icon.svelte';
  import SaveTemplateDialog from '../components/SaveTemplateDialog.svelte';

  type Obj = Record<string, unknown>;
  let templates = $state<TemplateSummary[]>([]);
  let templateId = $state('');
  let tpl = $state<Item<TemplateSpec> | null>(null);
  let markdown = $state('');
  let metadata = $state<Obj>({});
  let data = $state<Obj>({});
  let formats = $state({ pdf: true, docx: true });
  let key = uuid();
  let busy = $state(false);
  let findings = $state<Finding[]>([]);
  let failure = $state('');
  let result = $state<RenderResult | null>(null);
  let revising = $state<{ id: string; revision: number; title: string } | null>(null);
  let inferred = $state(new Set<string>());
  let planWarnings = $state<string[]>([]);
  let aiStep = $state('');
  let aiError = $state('');
  let saving = $state(false);

  const ctx = $derived({ components: Object.fromEntries((session.caps?.components ?? []).map((c) => [c.name, c.schema])) });
  let metaFields = $derived(tpl && session.caps ? metadataFields(tpl.core_metadata_schema ?? session.caps.core_metadata_schema, tpl.spec.metadata_schema, tpl.spec.languages, ctx) : []);
  let dataFields = $derived(tpl ? (objectFields(tpl.spec.data_schema, ctx) ?? []) : []);
  let bytesUsed = $derived(new TextEncoder().encode(markdown).length);
  let maxBytes = $derived(session.caps?.limits.max_markdown_bytes ?? 1 << 20);

  onMount(async () => {
    const list = await api.get<{ items: TemplateSummary[] }>('/templates');
    templates = list.items;
    const from = router.param('from');
    if (from) {
      const [src, doc] = await Promise.all([api.get<Source>(`/documents/${from}/source`), api.get<DocumentDetail>(`/documents/${from}`)]);
      revising = { id: from, revision: doc.latest_revision, title: doc.title };
      await pick(src.template_id);
      markdown = src.markdown;
      metadata = src.metadata ?? {};
      data = src.data ?? {};
      formats = { pdf: src.formats.includes('pdf'), docx: src.formats.includes('docx') };
    } else {
      const t = router.param('template') || templates[0]?.id || '';
      if (t) await pick(t);
    }
  });

  async function pick(id: string) {
    templateId = id;
    tpl = id ? await api.get<Item<TemplateSpec>>(`/templates/${encodeURIComponent(id)}`) : null;
    if (tpl && !revising) {
      const today = new Date().toISOString().slice(0, 10);
      metadata = { date: today, language: tpl.spec.languages[0], ...metadata };
    }
  }

  async function loadFile(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    if (f) markdown = await f.text();
  }

  async function render(e: SubmitEvent) {
    e.preventDefault();
    if (!tpl) return;
    busy = true;
    findings = [];
    failure = '';
    result = null;
    const fm = (['pdf', 'docx'] as const).filter((k) => formats[k]);
    const body: Obj = {
      template_id: templateId,
      markdown,
      metadata: prune(metadata) ?? {},
      data: prune(data) ?? {},
      formats: fm,
    };
    if (revising) {
      body.document_id = revising.id;
      body.expected_revision = revising.revision;
    } else body.idempotency_key = key;
    try {
      result = await work.run(api.post<RenderResult>('/render', body));
      key = uuid(); // the next render is a new document
      if (revising) revising = { ...revising, revision: result.revision };
      toasts.show(result.unchanged ? 'Nothing changed: Folio kept the existing revision.' : `Rendered revision ${result.revision}.`);
    } catch (err) {
      if (err instanceof ApiError) {
        failure = err.code === 'validation_failed' ? 'Fix these problems, then render again.' : err.message;
        findings = err.details;
      } else failure = String(err);
    } finally {
      busy = false;
    }
  }

  async function plan() {
    aiError = '';
    planWarnings = [];
    aiStep = 'starting';
    try {
      const p = await work.run(runJob<Plan>('/ai/document-plan', { markdown, template_id: revising ? templateId : '' }, (s) => (aiStep = s)));
      if (p.template_id !== templateId) await pick(p.template_id);
      metadata = { ...metadata, ...p.metadata };
      data = { ...data, ...p.data };
      inferred = new Set(p.inferred);
      planWarnings = p.warnings;
      toasts.show(p.inferred.length ? `Details filled in. Check the ${p.inferred.length} highlighted values.` : 'Details filled in from your text.');
      session.refreshAI();
    } catch (err) {
      aiError = err instanceof Error ? err.message : String(err);
    } finally {
      aiStep = '';
    }
  }
  let errorsByPath = $derived(Object.fromEntries(findings.filter((f) => f.path?.startsWith('/metadata') || f.path?.startsWith('/data')).map((f) => [f.path!, f.message])));
  let pdf = $derived(result?.files.find((f) => f.kind === 'pdf'));
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>{revising ? `Revise “${revising.title}”` : 'Create a document'}</h1>
      <p>{revising ? 'Change the text or the details and render a new revision. The earlier revisions stay available.' : 'Paste or upload Markdown, pick a template, fill in the details and render. Folio lays out your text and figures exactly as you write them.'}</p>
    </div>
  </div>

  <form class="layout" onsubmit={render}>
    <section class="sheet text" aria-labelledby="text-h">
      <div class="head">
        <h2 id="text-h" class="sheet-title">Your text</h2>
        <label class="btn small upload"><Icon name="upload" />Open a Markdown file<input type="file" accept=".md,.markdown,.txt,text/markdown,text/plain" onchange={loadFile} class="visually-hidden" /></label>
      </div>
      <label for="md" class="visually-hidden">Markdown</label>
      <textarea id="md" class="code md" bind:value={markdown} spellcheck="true" placeholder="# Executive summary&#10;&#10;Write or paste the document in Markdown…" aria-describedby="md-help"></textarea>
      <div class="foot">
        <span id="md-help" class="small muted num" class:over={bytesUsed > maxBytes}>{bytes(bytesUsed)} of {bytes(maxBytes)}</span>
        {#if tpl && !markdown && tpl.spec.body_skeleton}
          <button type="button" class="btn small" onclick={() => (markdown = tpl!.spec.body_skeleton)}>Start from the template’s outline</button>
        {/if}
      </div>
    </section>

    <section class="sheet details" aria-labelledby="details-h">
      <div class="head">
        <h2 id="details-h" class="sheet-title">Details</h2>
        {#if session.ai.enabled && !tpl?.inline}
          <button type="button" class="btn small" onclick={plan} disabled={!markdown.trim() || !!aiStep}>
            <Icon name="assistant" />{aiStep ? 'Reading your text…' : 'Fill in from my text'}
          </button>
        {/if}
      </div>
      {#if aiStep}<p class="note small" role="status">The assistant is {aiStep}.</p>{/if}
      {#if aiError}<p class="note error small" role="alert">{aiError}</p>{/if}
      {#if planWarnings.length}
        <div class="note warn small"><p><strong>The assistant was unsure about:</strong></p><ul>{#each planWarnings as w, i (i)}<li>{w}</li>{/each}</ul></div>
      {/if}

      {#if tpl?.inline}
        <!-- An agent sent this document's template with the render call: it
             is not in the picker; keep it, or save it as a regular template. -->
        <div class="field">
          <span class="label">Template</span>
          <p class="inline-tpl"><strong>Inline template</strong> <span class="help">sent by an agent with the document</span></p>
          {#if revising}<button type="button" class="btn small" onclick={() => (saving = true)}>Save as template</button>{/if}
        </div>
      {:else}
        <div class="field">
          <label for="tpl">Template</label>
          <select id="tpl" value={templateId} onchange={(e) => pick((e.currentTarget as HTMLSelectElement).value)} disabled={!!revising}>
            {#each templates as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
          </select>
          {#if tpl}<span class="help">{tpl.spec.description}</span>{/if}
        </div>
      {/if}

      {#if tpl}
        <h3 class="sub">Document</h3>
        <SchemaForm fields={metaFields} bind:value={metadata} idPrefix="meta" base="/metadata" {inferred} errors={errorsByPath} />
        {#if dataFields.length}
          <h3 class="sub">Tables and figures</h3>
          <p class="help small muted">Write every figure exactly as it should appear, for example €1,250.00. Folio does not calculate totals.</p>
          <SchemaForm fields={dataFields} bind:value={data} idPrefix="data" base="/data" {inferred} errors={errorsByPath} />
        {/if}
        <fieldset>
          <legend>Formats</legend>
          <label class="check"><input type="checkbox" bind:checked={formats.pdf} />PDF</label>
          <label class="check"><input type="checkbox" bind:checked={formats.docx} />Word (DOCX)</label>
        </fieldset>
      {:else if templates.length === 0}
        <p class="note">There are no templates yet. <a href={href('/templates/new')}>Create a template</a> first.</p>
      {/if}

      {#if failure}<p class="note error" role="alert">{failure}</p>{/if}
      <Findings {findings} />

      <button class="btn primary render" type="submit" disabled={busy || !tpl || !markdown.trim() || (!formats.pdf && !formats.docx)}>
        {busy ? 'Rendering…' : revising ? 'Render a new revision' : 'Render'}
      </button>
    </section>
  </form>

  {#if revising}<SaveTemplateDialog bind:open={saving} documentId={revising.id} />{/if}

  {#if result}
    <section class="sheet result" aria-labelledby="result-h">
      <div class="proof-col">
        <Proof src={`/ui/api/documents/${result.document_id}/pages/1.png?revision=${result.revision}`} alt="First page of the rendered document" eager marked />
      </div>
      <div class="result-body">
        <h2 id="result-h">{result.unchanged ? 'Already rendered' : `Revision ${result.revision} is ready`}</h2>
        <div class="buttons">
          {#each result.files.filter((f) => f.kind !== 'preview').sort((a) => (a.kind === 'pdf' ? -1 : 1)) as f (f.url)}
            <a class="btn" href={f.url} download={f.name}><Icon name="download" />Download {f.kind.toUpperCase()}</a>
          {/each}
          <a class="btn quiet" href={href('/documents/' + result.document_id)}>Open in documents</a>
        </div>
        <Findings findings={result.warnings} title="Warnings" />
        {#if pdf}<p class="small muted">Shareable link: <a href={pdf.url}>{pdf.name}</a></p>{/if}
      </div>
    </section>
  {/if}
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
    gap: var(--s-5);
    align-items: start;
  }
  .sheet + .sheet {
    margin-top: 0;
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--s-3);
  }
  .upload {
    cursor: pointer;
  }
  .upload:focus-within {
    box-shadow: var(--focus);
  }
  .md {
    min-height: 60vh;
    font-size: 0.875rem;
  }
  .foot {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--s-3);
    margin-top: var(--s-2);
  }
  .over {
    color: var(--error);
    font-weight: 700;
  }
  .details {
    position: sticky;
    top: calc(var(--masthead-h) + var(--s-4));
    max-height: calc(100vh - var(--masthead-h) - var(--s-6));
    overflow-y: auto;
  }
  .sub {
    font-size: var(--fs-1);
    margin: var(--s-5) 0 var(--s-3);
    padding-top: var(--s-3);
    border-top: 2px solid var(--ink);
  }
  .inline-tpl {
    margin: 0 0 var(--s-2);
  }
  .render {
    width: 100%;
    min-height: 3rem;
    margin-top: var(--s-3);
  }
  .result {
    margin-top: var(--s-5);
    display: grid;
    grid-template-columns: minmax(10rem, 16rem) 1fr;
    gap: var(--s-6);
    background: var(--table);
    border-style: dashed;
  }
  .result-body {
    display: grid;
    gap: var(--s-3);
    align-content: start;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s-2);
  }
  @media (max-width: 960px) {
    .layout,
    .result {
      grid-template-columns: 1fr;
    }
    .details {
      position: static;
      max-height: none;
    }
    .md {
      min-height: 40vh;
    }
  }
</style>
