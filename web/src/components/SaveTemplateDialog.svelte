<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Keep a document's inline template as a regular template: an id and a
     name; the document's latest text becomes the template's sample. -->
<script lang="ts">
  import { api, ApiError } from '../lib/api';
  import { toasts } from '../lib/toast.svelte';
  import { isInline } from '../lib/format';
  import type { SavedTemplate } from '../lib/types';
  import Dialog from './Dialog.svelte';

  let {
    open = $bindable(false),
    documentId,
    onsaved,
  }: { open?: boolean; documentId: string; onsaved?: (t: SavedTemplate) => void } = $props();
  const uid = $props.id();
  let id = $state('');
  let name = $state('');
  let busy = $state(false);
  let error = $state('');
  const slug = /^[a-z0-9][a-z0-9-]{1,62}$/;
  let idProblem = $derived(
    id === '' ? '' : !slug.test(id) ? 'Use 2 to 63 lowercase letters, digits and hyphens.' : id === 'inline' || isInline(id) ? 'Ids starting with inline- are reserved.' : '',
  );

  async function save() {
    busy = true;
    error = '';
    try {
      const t = await api.post<SavedTemplate>(`/documents/${encodeURIComponent(documentId)}/save-template`, { id, name: name.trim() });
      toasts.show(`Saved as the template “${name.trim()}”.`);
      open = false;
      onsaved?.(t);
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog bind:open title="Save as a template" confirm="Save template" {busy} disabled={!id || !name.trim() || !!idProblem} onconfirm={save}>
  <p>The new template keeps this document’s layout, sections and schemas; its latest text becomes the template’s sample.</p>
  <div class="field">
    <label for="{uid}-id">Template id</label>
    <input id="{uid}-id" type="text" bind:value={id} autocomplete="off" spellcheck="false" aria-describedby="{uid}-id-help" aria-invalid={idProblem ? 'true' : undefined} />
    <span id="{uid}-id-help" class={idProblem ? 'error-text' : 'help'}>{idProblem || 'For example client-letter.'}</span>
  </div>
  <div class="field">
    <label for="{uid}-name">Name</label>
    <input id="{uid}-name" type="text" bind:value={name} autocomplete="off" />
  </div>
  {#if error}<p class="note error" role="alert">{error}</p>{/if}
</Dialog>
