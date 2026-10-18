<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Brand editor: colour pickers, bundled-font specimens, logo, header and
     footer, with a live specimen page; the assistant can propose a brand
     from a logo or a website. -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { router, href } from '../lib/router.svelte';
  import { session } from '../lib/session.svelte';
  import { toasts } from '../lib/toast.svelte';
  import { work } from '../lib/work.svelte';
  import { runJob } from '../lib/jobs';
  import { specimenFamily } from '../lib/fonts';
  import type { Asset, BrandSpec, BrandSuggestion, Finding, Item } from '../lib/types';
  import Findings from '../components/Findings.svelte';
  import Icon from '../components/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';

  let { id }: { id: string } = $props();
  const isNew = $derived(!id);
  const roles = [
    ['primary', 'Primary', 'Headings and table headers'],
    ['secondary', 'Secondary', 'A darker companion colour'],
    ['accent', 'Accent', 'Highlights and rules'],
    ['text', 'Text', 'Body text'],
    ['muted', 'Muted', 'Captions, footers, secondary text'],
    ['background', 'Background', 'The page'],
  ] as const;

  let b = $state<BrandSpec | null>(null);
  let latest = $state(0);
  let archived = $state(false);
  let assets = $state<Asset[]>([]);
  let error = $state('');
  let saving = $state(false);
  let findings = $state<Finding[]>([]);
  let dirty = $state(false);
  let confirmArchive = $state(false);
  let fam = $state({ heading: 'inherit', body: 'inherit' });
  let spec = $state<Record<string, string>>({ heading: 'inherit', body: 'inherit', mono: 'inherit' });

  // Assistant
  let aiMode = $state<'' | 'logo' | 'url'>('');
  let aiAsset = $state('');
  let aiURL = $state('');
  let aiStep = $state('');
  let aiError = $state('');
  let suggestion = $state<BrandSuggestion | null>(null);
  let suggestionJob = '';

  function blank(): BrandSpec {
    return {
      id: '', name: '',
      colors: { primary: '#1F4E79', secondary: '#0B1F33', accent: '#C8246B', text: '#1C2421', muted: '#5B6662', background: '#FFFFFF' },
      fonts: { heading: 'Inter', body: 'Source Serif 4', mono: 'JetBrains Mono' },
      header: { text: '', show_logo: false }, footer: { text: '', legal: '', page_numbers: true }, docx: {},
    };
  }

  onMount(async () => {
    try {
      assets = (await api.get<{ items: Asset[] }>('/assets?limit=200')).items;
      if (isNew) b = blank();
      else {
        const it = await api.get<Item<BrandSpec>>(`/brands/${encodeURIComponent(id)}`);
        b = it.spec;
        latest = it.latest_version;
        archived = it.archived;
      }
    } catch (e) {
      error = e instanceof ApiError && e.status === 404 ? `There is no brand “${id}”.` : e instanceof Error ? e.message : String(e);
    }
  });
  let first = true;
  $effect(() => {
    if (!b) return;
    JSON.stringify(b);
    if (!first) dirty = true;
    first = false;
  });
  $effect(() => {
    if (!b) return;
    const h = b.fonts.heading;
    const bd = b.fonts.body;
    specimenFamily(session.caps, h).then((v) => (fam.heading = v));
    specimenFamily(session.caps, bd).then((v) => (fam.body = v));
    for (const k of ['heading', 'body', 'mono'] as const) specimenFamily(session.caps, b.fonts[k]).then((v) => (spec[k] = v));
  });

  function luminance(hex: string): number {
    const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * c[0]! + 0.7152 * c[1]! + 0.0722 * c[2]!;
  }
  function contrast(a: string, c: string): number {
    if (!/^#[0-9a-f]{6}$/i.test(a) || !/^#[0-9a-f]{6}$/i.test(c)) return 21;
    const [x, y] = [luminance(a), luminance(c)].sort((p, q) => q - p);
    return (x! + 0.05) / (y! + 0.05);
  }
  let textContrast = $derived(b ? contrast(b.colors.text, b.colors.background) : 21);
  let mutedContrast = $derived(b ? contrast(b.colors.muted, b.colors.background) : 21);
  let logoURL = $derived(b?.logo ? `/ui/api/assets/${b.logo.asset_id}/content` : '');

  async function save() {
    if (!b) return;
    saving = true;
    findings = [];
    error = '';
    try {
      const body = { brand: $state.snapshot(b), expected_version: latest };
      const res = isNew ? await api.post<{ id: string; version: number }>('/brands', body) : await api.put<{ id: string; version: number }>(`/brands/${encodeURIComponent(id)}`, body);
      toasts.show(`Saved version ${res.version} of ${b.name}.`);
      dirty = false;
      latest = res.version;
      if (isNew) router.navigate('/brands/' + res.id, { replace: true });
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.code === 'conflict' ? 'Someone saved a newer version while you were editing. Reload the page and apply your change again.' : e.message;
        findings = e.details;
      } else error = String(e);
    } finally {
      saving = false;
    }
  }
  async function archive() {
    try {
      await api.post(`/brands/${encodeURIComponent(id)}/archive`, { archived: !archived });
      archived = !archived;
      toasts.show(archived ? 'Archived.' : 'Restored.');
    } catch (e) {
      toasts.show(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      confirmArchive = false;
    }
  }
  async function uploadLogo(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!f || !b) return;
    const fd = new FormData();
    fd.append('file', f);
    try {
      const a = await api.post<Asset>('/assets', fd);
      assets = [a, ...assets];
      b.logo = { asset_id: a.asset_id, width_mm: b.logo?.width_mm ?? 35 };
      b.header.show_logo = true;
      aiAsset = a.asset_id;
      toasts.show(`Uploaded ${a.name}.`);
    } catch (err) {
      toasts.show(err instanceof Error ? err.message : String(err), 'error');
    }
  }

  async function suggest(e: SubmitEvent) {
    e.preventDefault();
    aiError = '';
    aiStep = 'starting';
    suggestion = null;
    try {
      const body = aiMode === 'logo' ? { asset_id: aiAsset } : { url: aiURL };
      let jobId = '';
      const res = await work.run(
        (async () => {
          const { job_id } = await api.post<{ job_id: string }>('/ai/brand-suggest', body);
          jobId = job_id;
          for (;;) {
            await new Promise((r) => setTimeout(r, 2000));
            const j = await api.get<{ state: string; step?: string; result?: BrandSuggestion; error?: { message: string } }>(`/ai/jobs/${job_id}`);
            if (j.state === 'done') return j.result!;
            if (j.state === 'error') throw new Error(j.error?.message ?? 'The assistant failed.');
            aiStep = j.step || 'working';
          }
        })(),
      );
      suggestion = res;
      suggestionJob = jobId;
      session.refreshAI();
    } catch (err) {
      aiError = err instanceof Error ? err.message : String(err);
    } finally {
      aiStep = '';
    }
  }
  function applySuggestion() {
    if (!b || !suggestion) return;
    const s = suggestion.brand;
    b.name = b.name || s.name;
    if (isNew && !b.id) b.id = s.id;
    b.colors = { ...s.colors };
    b.fonts = { ...s.fonts };
    b.header.text = s.header.text || b.header.text;
    b.footer.text = s.footer.text || b.footer.text;
    if (s.logo) {
      b.logo = { ...s.logo };
      b.header.show_logo = true;
    }
    toasts.show('Applied the proposal. Check it, then save.');
  }
  async function useLogo(index: number) {
    try {
      const a = await api.post<Asset>('/ai/brand-logo', { job_id: suggestionJob, index });
      assets = [a, ...assets];
      if (b) {
        b.logo = { asset_id: a.asset_id, width_mm: b.logo?.width_mm ?? 35 };
        b.header.show_logo = true;
      }
      toasts.show('Saved the logo as an image and set it on the brand.');
    } catch (err) {
      toasts.show(err instanceof Error ? err.message : String(err), 'error');
    }
  }
  const fonts = $derived(session.caps?.fonts.map((f) => f.family) ?? []);
</script>

{#if error && !b}
  <div class="page"><p class="note error">{error}</p></div>
{:else if b}
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{b.name || (isNew ? 'New brand' : id)}</h1>
        <p>{isNew ? 'Not saved yet.' : `Version ${latest}${archived ? ', archived' : ''}.`}{dirty ? ' Unsaved changes.' : ''}</p>
      </div>
      <div class="actions">
        {#if session.ai.enabled}
          <button class="btn" onclick={() => (aiMode = aiMode ? '' : 'logo')} aria-expanded={!!aiMode}><Icon name="assistant" />Suggest with the assistant</button>
        {/if}
        {#if !isNew}<button class="btn" onclick={() => (confirmArchive = true)}>{archived ? 'Restore' : 'Archive'}</button>{/if}
        <button class="btn primary" onclick={save} disabled={saving || (!dirty && !isNew)}>{saving ? 'Saving…' : isNew ? 'Create brand' : 'Save as new version'}</button>
      </div>
    </div>
    {#if error}<p class="note error msg" role="alert">{error}</p>{/if}
    {#if findings.length}<div class="msg"><Findings {findings} title="Fix these before saving" /></div>{/if}

    {#if aiMode}
      <form class="sheet ai" onsubmit={suggest} aria-labelledby="ai-h">
        <h2 id="ai-h" class="sheet-title">Suggest a brand</h2>
        <div class="seg" role="radiogroup" aria-label="Start from">
          <label class="choice"><input type="radio" bind:group={aiMode} value="logo" />From a logo</label>
          <label class="choice"><input type="radio" bind:group={aiMode} value="url" />From a website</label>
        </div>
        {#if aiMode === 'logo'}
          <div class="field">
            <label for="ai-asset">Logo</label>
            <select id="ai-asset" bind:value={aiAsset}>
              <option value="">Choose an uploaded image…</option>
              {#each assets as a (a.asset_id)}<option value={a.asset_id}>{a.name}</option>{/each}
            </select>
            <span class="help">Folio reads the logo’s main colours itself; the model maps them to roles and picks fonts{session.ai.vision ? ' and also sees the logo' : ''}.</span>
          </div>
        {:else}
          <div class="field">
            <label for="ai-url">Website address</label>
            <input id="ai-url" type="url" bind:value={aiURL} placeholder="https://example.com" />
            <span class="help">Folio reads the page’s title, colours and logo candidates (https only, public addresses only). Nothing is downloaded from it until you pick a logo.</span>
          </div>
        {/if}
        {#if aiStep}<p class="note" role="status">The assistant is {aiStep}.</p>{/if}
        {#if aiError}<p class="note error" role="alert">{aiError}</p>{/if}
        <button class="btn primary" type="submit" disabled={!!aiStep || (aiMode === 'logo' ? !aiAsset : !aiURL.startsWith('https://'))}>{aiStep ? 'Working…' : 'Suggest'}</button>

        {#if suggestion}
          <div class="proposal">
            <h3>Proposal: {suggestion.brand.name}</h3>
            <div class="chips">
              {#each roles as [k, l] (k)}<span class="sw"><i style:background={suggestion.brand.colors[k]}></i>{l} <span class="code small">{suggestion.brand.colors[k]}</span></span>{/each}
            </div>
            <p class="small">Fonts: {suggestion.brand.fonts.heading} for headings, {suggestion.brand.fonts.body} for text.</p>
            {#if suggestion.swatches?.length}
              <p class="small muted">Colours found in the logo: {#each suggestion.swatches as s, i (i)}<span class="mini"><i style:background={s.hex}></i>{s.hex}</span>{/each}</p>
            {/if}
            {#if suggestion.evidence?.images.length}
              <p class="small">Logo candidates found on the page:</p>
              <ul class="cands">
                {#each suggestion.evidence.images as url, i (url)}
                  <li class:best={i === suggestion.logo_index}>
                    <span class="code small">{url}</span>
                    <button type="button" class="btn small" onclick={() => useLogo(i)}>{i === suggestion.logo_index ? 'Use the suggested logo' : 'Use this one'}</button>
                  </li>
                {/each}
              </ul>
            {/if}
            <Findings findings={suggestion.findings} />
            <button type="button" class="btn" onclick={applySuggestion}>Apply to the form</button>
          </div>
        {/if}
      </form>
    {/if}

    <div class="cols">
      <div>
        <section class="sheet">
          <h2 class="sheet-title">Name</h2>
          <div class="field"><label for="b-name">Name</label><input id="b-name" type="text" bind:value={b.name} /></div>
          <div class="field"><label for="b-id">Brand id</label><input id="b-id" type="text" bind:value={b.id} disabled={!isNew} />
            <span class="help">{isNew ? 'Lowercase letters, digits and hyphens; templates refer to it.' : 'The id cannot change.'}</span></div>
        </section>
        <section class="sheet">
          <h2 class="sheet-title">Colours</h2>
          <div class="colors">
            {#each roles as [k, l, help] (k)}
              <div class="color">
                <input type="color" id="c-{k}" bind:value={b.colors[k]} aria-label="{l} colour picker" aria-describedby="c-{k}-help" />
                <div>
                  <label for="c-{k}-hex">{l}</label>
                  <input id="c-{k}-hex" type="text" class="code" bind:value={b.colors[k]} maxlength="7" aria-invalid={/^#[0-9a-fA-F]{6}$/.test(b.colors[k]) ? undefined : 'true'} />
                  <span class="help" id="c-{k}-help">{help}</span>
                </div>
              </div>
            {/each}
          </div>
          {#if textContrast < 4.5}<p class="note warn small">Text on the background has a contrast of {textContrast.toFixed(1)}:1; aim for at least 4.5:1 so documents stay readable.</p>{/if}
          {#if mutedContrast < 3}<p class="note warn small">Muted text is hard to read on the background ({mutedContrast.toFixed(1)}:1).</p>{/if}
        </section>
        <section class="sheet">
          <h2 class="sheet-title">Fonts</h2>
          {#each [['heading', 'Headings'], ['body', 'Text'], ['mono', 'Code']] as [k, l] (k)}
            <div class="field">
              <label for="f-{k}">{l}</label>
              <select id="f-{k}" bind:value={b.fonts[k as 'heading']} aria-describedby="f-{k}-spec">{#each fonts as f (f)}<option value={f}>{f}</option>{/each}</select>
              <p class="specimen" id="f-{k}-spec" style:font-family={spec[k ?? 'body']}>Quarterly report 2026: 1,234.50 € (Àéîõü)</p>
            </div>
          {/each}
        </section>
        <section class="sheet">
          <h2 class="sheet-title">Logo, header and footer</h2>
          <div class="field">
            <label for="b-logo">Logo</label>
            <select id="b-logo" value={b.logo?.asset_id ?? ''} onchange={(e) => {
              const v = (e.currentTarget as HTMLSelectElement).value;
              b!.logo = v ? { asset_id: v, width_mm: b!.logo?.width_mm ?? 35 } : undefined;
            }}>
              <option value="">No logo</option>
              {#each assets.filter((a) => a.mime !== 'image/jpeg') as a (a.asset_id)}<option value={a.asset_id}>{a.name}</option>{/each}
            </select>
            <label class="btn small upload"><Icon name="upload" />Upload a logo (PNG or SVG)<input type="file" accept=".png,.svg,image/png,image/svg+xml" class="visually-hidden" onchange={uploadLogo} /></label>
          </div>
          {#if b.logo}
            <div class="field narrow"><label for="b-w">Logo width (mm)</label><input id="b-w" type="number" min="5" max="120" bind:value={b.logo.width_mm} /></div>
          {/if}
          <div class="field"><label for="b-ht">Header text</label><input id="b-ht" type="text" bind:value={b.header.text} /></div>
          <label class="check"><input type="checkbox" bind:checked={b.header.show_logo} disabled={!b.logo} />Show the logo in the header</label>
          <div class="field"><label for="b-ft">Footer text</label><input id="b-ft" type="text" bind:value={b.footer.text} /></div>
          <div class="field"><label for="b-fl">Legal line</label><input id="b-fl" type="text" bind:value={b.footer.legal} placeholder="Company name, registration number, address" /></div>
          <label class="check"><input type="checkbox" bind:checked={b.footer.page_numbers} />Page numbers</label>
        </section>
      </div>

      <aside class="preview" aria-label="Specimen page">
        <div class="page-mock" style:background={b.colors.background} style:color={b.colors.text} style:font-family={fam.body}>
          <div class="mock-head" style:color={b.colors.muted}>
            {#if b.logo && b.header.show_logo}<img src={logoURL} alt="" class="mock-logo" />{/if}
            <span>{b.header.text}</span>
          </div>
          <div class="mock-rule" style:background={b.colors.accent}></div>
          <h2 class="mock-h" style:color={b.colors.primary} style:font-family={fam.heading}>Website redesign proposal</h2>
          <p class="mock-sub" style:color={b.colors.secondary} style:font-family={fam.heading}>Prepared for ACME Corp</p>
          <p class="mock-p">We propose to redesign the public website so that customers find products faster and the team can publish without developer help.</p>
          <table class="mock-t">
            <thead><tr><th style:background={b.colors.primary} style:color={b.colors.background}>Item</th><th style:background={b.colors.primary} style:color={b.colors.background}>Amount</th></tr></thead>
            <tbody>
              <tr><td>Discovery and architecture</td><td class="num">€4,800.00</td></tr>
              <tr><td>Design system</td><td class="num">€7,800.00</td></tr>
              <tr class="tot"><td style:color={b.colors.primary}>Total</td><td class="num" style:color={b.colors.primary}>€12,600.00</td></tr>
            </tbody>
          </table>
          <div class="mock-foot" style:color={b.colors.muted}>
            <span>{b.footer.text}{b.footer.legal ? ` ${b.footer.legal}` : ''}</span>{#if b.footer.page_numbers}<span>1</span>{/if}
          </div>
        </div>
        <p class="small muted">A quick specimen in your browser. Open a template that uses this brand to see a real proof.</p>
      </aside>
    </div>
  </div>
  <Dialog bind:open={confirmArchive} title={archived ? 'Restore this brand?' : 'Archive this brand?'} confirm={archived ? 'Restore' : 'Archive'} onconfirm={archive}>
    <p>{archived ? 'Templates can use it again.' : 'Templates that use it can no longer render new documents until you restore it or pick another brand.'}</p>
  </Dialog>
{/if}

<style>
  .msg {
    margin-bottom: var(--s-4);
  }
  .ai {
    margin-bottom: var(--s-6);
    box-shadow: var(--sheet-shadow);
    max-width: 52rem;
  }
  .seg {
    display: flex;
    gap: var(--s-2);
    margin-bottom: var(--s-4);
  }
  .choice {
    display: flex;
    gap: var(--s-2);
    align-items: center;
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
  .proposal {
    margin-top: var(--s-5);
    padding-top: var(--s-4);
    border-top: 1px solid var(--rule);
    display: grid;
    gap: var(--s-3);
    justify-items: start;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s-3);
  }
  .sw,
  .mini {
    display: inline-flex;
    align-items: center;
    gap: var(--s-1);
    font-size: var(--fs-0);
    font-weight: 600;
  }
  .sw i,
  .mini i {
    width: 1.1rem;
    height: 1.1rem;
    border: 1px solid var(--rule-strong);
  }
  .mini {
    margin-left: var(--s-2);
  }
  .cands {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--s-2);
    width: 100%;
  }
  .cands li {
    display: flex;
    justify-content: space-between;
    gap: var(--s-3);
    align-items: center;
    padding: var(--s-2);
    border: 1px solid var(--rule);
  }
  .cands li.best {
    border-color: var(--magenta);
  }
  .cands .code {
    overflow-wrap: anywhere;
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--s-6);
    align-items: start;
  }
  .colors {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--s-4);
  }
  .color {
    display: flex;
    gap: var(--s-3);
    align-items: flex-start;
  }
  .color label {
    font-weight: 600;
    font-size: var(--fs-0);
  }
  input[type='color'] {
    flex: none;
    width: 3rem;
    height: 3rem;
    padding: 2px;
    border: 1px solid var(--rule-strong);
    border-radius: var(--radius-control);
    background: var(--paper);
  }
  .color .help {
    display: block;
    font-size: var(--fs-0);
    color: var(--ink-2);
  }
  .upload {
    justify-self: start;
    margin-top: var(--s-2);
    cursor: pointer;
  }
  .upload:focus-within {
    box-shadow: var(--focus);
  }
  .narrow {
    max-width: 10rem;
  }
  .check {
    display: flex;
    margin-bottom: var(--s-3);
  }
  .preview {
    position: sticky;
    top: calc(var(--masthead-h) + var(--s-4));
    display: grid;
    gap: var(--s-3);
  }
  .page-mock {
    aspect-ratio: 210 / 297;
    padding: 7% 9%;
    box-shadow: var(--sheet-shadow);
    display: flex;
    flex-direction: column;
    gap: 3%;
    font-size: 0.85rem;
    overflow: hidden;
  }
  .mock-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: 0.75rem;
  }
  .mock-logo {
    max-height: 2rem;
    max-width: 45%;
    object-fit: contain;
  }
  .mock-rule {
    height: 2px;
  }
  .mock-h {
    font-size: 1.6rem;
    line-height: 1.1;
    margin-top: 6%;
  }
  .mock-sub {
    font-weight: 600;
  }
  .mock-t {
    border-collapse: collapse;
    width: 100%;
    font-size: 0.8rem;
  }
  .mock-t th,
  .mock-t td {
    text-align: left;
    padding: 0.3rem 0.5rem;
  }
  .mock-t td {
    border-bottom: 1px solid rgba(0, 0, 0, 0.12);
  }
  .mock-t .tot td {
    font-weight: 700;
  }
  .mock-t .num,
  .mock-t th:last-child {
    text-align: right;
  }
  .specimen {
    font-size: var(--fs-2);
    line-height: 1.3;
    padding: var(--s-2) 0 0;
    overflow-wrap: anywhere;
  }
  .mock-foot {
    margin-top: auto;
    display: flex;
    justify-content: space-between;
    font-size: 0.7rem;
  }
  @media (max-width: 960px) {
    .cols {
      grid-template-columns: 1fr;
    }
    .preview {
      position: static;
    }
    .colors {
      grid-template-columns: 1fr;
    }
  }
</style>
