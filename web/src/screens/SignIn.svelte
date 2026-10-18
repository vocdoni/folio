<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { ApiError } from '../lib/api';
  import { session } from '../lib/session.svelte';
  import ColorBar from '../components/ColorBar.svelte';

  let password = $state('');
  let error = $state('');
  let busy = $state(false);
  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!password) {
      error = 'Enter the password.';
      return;
    }
    busy = true;
    error = '';
    try {
      await session.signIn(password);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'rate_limited') {
        error = `Too many attempts. Wait ${err.retryAfter || 60} seconds, then try again.`;
      } else if (err instanceof ApiError && err.code === 'unauthorized') {
        error = 'That password is not right. Check it and try again.';
      } else {
        error = err instanceof Error ? err.message : 'Signing in failed.';
      }
    } finally {
      busy = false;
    }
  }
</script>

<main class="bench">
  <form class="sheet" onsubmit={submit} novalidate>
    <div class="mark"><span class="name">Folio</span><ColorBar busy={busy} /></div>
    <h1>Sign in</h1>
    <p class="muted">The proofing room for the documents your agents make.</p>
    <div class="field">
      <label for="password">Password</label>
      <input id="password" type="password" autocomplete="current-password" bind:value={password}
        aria-invalid={error ? 'true' : undefined} aria-describedby="password-help {error ? 'password-error' : ''}" />
      <span class="help" id="password-help">The password set in FOLIO_UI_PASSWORD on this server.</span>
      {#if error}<span class="error-text" id="password-error" role="alert">{error}</span>{/if}
    </div>
    <button class="btn primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</main>

<style>
  .bench {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: var(--s-5);
  }
  form {
    width: min(26rem, 100%);
    padding: var(--s-6);
    box-shadow: var(--sheet-shadow);
    display: grid;
    gap: var(--s-3);
  }
  .mark {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    margin-bottom: var(--s-4);
  }
  .name {
    font-size: 1.5rem;
    font-weight: 850;
    letter-spacing: -0.03em;
  }
  h1 {
    font-size: var(--fs-4);
  }
  p {
    margin-bottom: var(--s-3);
  }
  .btn {
    justify-self: start;
    min-width: 8rem;
  }
</style>
