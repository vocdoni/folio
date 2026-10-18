<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- The masthead: wordmark with the colour bar, the sections, the live
     feed's state and sign out. The current section carries a magenta
     registration mark. -->
<script lang="ts">
  import { router, href } from '../lib/router.svelte';
  import { live } from '../lib/live.svelte';
  import { session } from '../lib/session.svelte';
  import ColorBar from './ColorBar.svelte';
  import Icon from './Icon.svelte';

  let { busy = false }: { busy?: boolean } = $props();
  const items = [
    { path: '/', label: 'Overview', names: ['overview'] },
    { path: '/activity', label: 'Activity', names: ['activity'] },
    { path: '/documents', label: 'Documents', names: ['documents', 'document'] },
    { path: '/create', label: 'Create', names: ['create'] },
    { path: '/templates', label: 'Templates', names: ['templates', 'studio', 'studio-new'] },
    { path: '/brands', label: 'Brands', names: ['brands', 'brand', 'brand-new'] },
    { path: '/assets', label: 'Images', names: ['assets'] },
    { path: '/settings', label: 'Settings', names: ['settings'] },
  ];
  let menuOpen = $state(false);
  $effect(() => {
    void router.path;
    menuOpen = false;
  });
  const liveText: Record<string, string> = { live: 'Live', connecting: 'Connecting', retrying: 'Reconnecting', off: 'Offline' };
</script>

<header class="masthead">
  <a class="wordmark" href={href('/')} aria-label="Folio overview">
    <span class="name">Folio</span>
    <ColorBar {busy} />
  </a>
  <button class="btn quiet menu-toggle" aria-expanded={menuOpen} aria-controls="site-nav" onclick={() => (menuOpen = !menuOpen)}>
    <Icon name={menuOpen ? 'close' : 'menu'} />Menu
  </button>
  <nav id="site-nav" class:open={menuOpen} aria-label="Sections">
    <ul>
      {#each items as it (it.path)}
        {@const current = it.names.includes(router.route.name)}
        <li><a href={href(it.path)} aria-current={current ? 'page' : undefined} class:current>{it.label}</a></li>
      {/each}
    </ul>
    <div class="tools">
      <span class="live {live.status}" title="Live activity feed">{liveText[live.status]}</span>
      <button class="btn quiet small" onclick={() => session.signOut()}>Sign out</button>
    </div>
  </nav>
</header>

<style>
  .masthead {
    position: sticky;
    top: 0;
    z-index: 30;
    display: flex;
    align-items: center;
    gap: var(--s-5);
    min-height: var(--masthead-h);
    padding: 0 clamp(1rem, 3vw, 2.5rem);
    background: var(--paper);
    border-bottom: 1px solid var(--rule);
  }
  .wordmark {
    display: inline-flex;
    align-items: center;
    gap: var(--s-3);
    color: var(--ink);
    text-decoration: none;
  }
  .name {
    font-size: 1.375rem;
    font-weight: 850;
    letter-spacing: -0.03em;
  }
  nav {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s-4);
  }
  ul {
    display: flex;
    gap: var(--s-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  nav a {
    position: relative;
    display: inline-flex;
    align-items: center;
    min-height: 2.25rem;
    padding: 0 var(--s-3);
    color: var(--ink-2);
    font-weight: 600;
    text-decoration: none;
    border-radius: var(--radius-control);
  }
  nav a:hover {
    color: var(--ink);
    background: var(--paper-2);
  }
  nav a.current {
    color: var(--ink);
    padding-left: calc(var(--s-3) + 14px);
  }
  /* Registration mark: a ring with crosshairs. */
  nav a.current::before {
    content: '';
    position: absolute;
    left: var(--s-2);
    top: 50%;
    width: 11px;
    height: 11px;
    margin-top: -5.5px;
    border: 1.5px solid var(--magenta);
    border-radius: 50%;
    background:
      linear-gradient(var(--magenta), var(--magenta)) center / 1.5px 100% no-repeat,
      linear-gradient(var(--magenta), var(--magenta)) center / 100% 1.5px no-repeat;
  }
  .tools {
    display: flex;
    align-items: center;
    gap: var(--s-3);
  }
  .live {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
    font-size: var(--fs-0);
    font-weight: 600;
    color: var(--ink-2);
  }
  .live::before {
    content: '';
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--rule-strong);
  }
  .live.live::before {
    background: var(--cyan);
  }
  .live.retrying::before {
    background: var(--yellow);
  }
  .menu-toggle {
    display: none;
    margin-left: auto;
  }
  @media (max-width: 1120px) {
    .menu-toggle {
      display: inline-flex;
    }
    nav {
      display: none;
      position: absolute;
      top: 100%;
      left: 0;
      right: 0;
      flex-direction: column;
      align-items: stretch;
      padding: var(--s-3) clamp(1rem, 3vw, 2.5rem) var(--s-4);
      background: var(--paper);
      border-bottom: 1px solid var(--rule);
      box-shadow: var(--sheet-shadow);
    }
    nav.open {
      display: flex;
    }
    ul {
      flex-direction: column;
    }
    nav a {
      width: 100%;
      min-height: 2.75rem;
    }
    .tools {
      justify-content: space-between;
      border-top: 1px solid var(--rule);
      padding-top: var(--s-3);
    }
  }
</style>
