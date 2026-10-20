<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
  import { ApiError } from '../lib/api';
  import { session } from '../lib/session.svelte';
  import { router } from '../lib/router.svelte';
  import { ssoError, ssoHref } from '../lib/signin';
  import ColorBar from '../components/ColorBar.svelte';

  // A refused SSO sign-in comes back as /ui/?error=<code>: say why once and
  // drop the code from the address.
  const refused = ssoError(router.param('error'));
  if (refused) router.setParams({ error: '' });
  // Only the methods this server offers (an older answer without them: password).
  const methods = $derived(session.info.methods ?? { password: true, oidc: null });
  // A plain link, not a form: the provider round trip is a top-level navigation.
  const sso = $derived(ssoHref(router.path, router.search));

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
  <div class="sheet">
    <div class="mark"><span class="name">Folio</span><ColorBar busy={busy} /></div>
    <h1>Sign in</h1>
    <p class="muted">The proofing room for the documents your agents make.</p>
    {#if refused}<p class="note error" role="alert">{refused}</p>{/if}
    {#if methods.oidc}
      <a class="btn primary sso" href={sso}>{methods.oidc.name}</a>
    {/if}
    {#if methods.password}
      {#if methods.oidc}<p class="or small muted">Or use the password</p>{/if}
      <form onsubmit={submit} novalidate>
        <div class="field">
          <label for="password">Password</label>
          <input id="password" type="password" autocomplete="current-password" bind:value={password}
            aria-invalid={error ? 'true' : undefined} aria-describedby="password-help {error ? 'password-error' : ''}" />
          <span class="help" id="password-help">The password set in FOLIO_UI_PASSWORD on this server.</span>
          {#if error}<span class="error-text" id="password-error" role="alert">{error}</span>{/if}
        </div>
        <button class="btn" class:primary={!methods.oidc} type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
    {/if}
  </div>
</main>

<style>
  .bench {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: var(--s-5);
  }
  .sheet {
    width: min(26rem, 100%);
    padding: var(--s-6);
    box-shadow: var(--sheet-shadow);
    display: grid;
    gap: var(--s-3);
  }
  form {
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
  .or {
    margin: var(--s-2) 0 0;
  }
  .btn {
    justify-self: start;
    min-width: 8rem;
  }
  .btn.sso {
    justify-self: stretch;
  }
</style>
