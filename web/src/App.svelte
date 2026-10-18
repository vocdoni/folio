<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { router } from './lib/router.svelte';
  import { session } from './lib/session.svelte';
  import { live } from './lib/live.svelte';
  import { work } from './lib/work.svelte';
  import Masthead from './components/Masthead.svelte';
  import Toasts from './components/Toasts.svelte';
  import SignIn from './screens/SignIn.svelte';
  import Overview from './screens/Overview.svelte';
  import Activity from './screens/Activity.svelte';
  import Documents from './screens/Documents.svelte';
  import Create from './screens/Create.svelte';
  import Templates from './screens/Templates.svelte';
  import Studio from './screens/Studio.svelte';
  import Brands from './screens/Brands.svelte';
  import BrandEditor from './screens/BrandEditor.svelte';
  import Assets from './screens/Assets.svelte';
  import Settings from './screens/Settings.svelte';
  import NotFound from './screens/NotFound.svelte';

  onMount(() => {
    session.check();
  });
  $effect(() => {
    if (session.state === 'signed-in') live.start();
    else live.stop();
  });
  const titles: Record<string, string> = {
    overview: 'Overview', activity: 'Activity', documents: 'Documents', document: 'Document', create: 'Create a document',
    templates: 'Templates', studio: 'Template studio', 'studio-new': 'New template', brands: 'Brands', brand: 'Brand',
    'brand-new': 'New brand', assets: 'Images', settings: 'Settings', 'not-found': 'Not found',
  };
  $effect(() => {
    document.title = session.state === 'signed-in' ? `${titles[router.route.name] ?? 'Folio'} · Folio` : 'Sign in · Folio';
  });
</script>

{#if session.state === 'checking'}
  <div class="checking" aria-busy="true"><span class="visually-hidden">Loading Folio</span></div>
{:else if session.state === 'signed-out'}
  <SignIn />
{:else}
  <a class="skip-link" href="#main">Skip to content</a>
  <Masthead busy={work.busy} />
  <main id="main" tabindex="-1">
    {#if router.route.name === 'overview'}<Overview />
    {:else if router.route.name === 'activity'}<Activity />
    {:else if router.route.name === 'documents' || router.route.name === 'document'}<Documents openId={router.route.params.id ?? ''} />
    {:else if router.route.name === 'create'}<Create />
    {:else if router.route.name === 'templates'}<Templates />
    {:else if router.route.name === 'studio' || router.route.name === 'studio-new'}
      {#key router.route.params.id ?? 'new'}<Studio id={router.route.params.id ?? ''} />{/key}
    {:else if router.route.name === 'brands'}<Brands />
    {:else if router.route.name === 'brand' || router.route.name === 'brand-new'}
      {#key router.route.params.id ?? 'new'}<BrandEditor id={router.route.params.id ?? ''} />{/key}
    {:else if router.route.name === 'assets'}<Assets />
    {:else if router.route.name === 'settings'}<Settings />
    {:else}<NotFound />{/if}
  </main>
{/if}
<Toasts />

<style>
  .checking {
    min-height: 100vh;
  }
  main:focus {
    outline: none;
  }
</style>
