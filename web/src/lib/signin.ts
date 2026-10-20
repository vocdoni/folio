// SPDX-License-Identifier: AGPL-3.0-only
// SSO sign-in helpers (pure: unit-tested without a DOM): the link that
// starts the provider round trip, and the message for each error code the
// callback sends back as /ui/?error=<code>.

import { ROOT } from './routes';
import type { SignInMethods } from './types';

/** ssoHref is the sign-in link; Folio returns to the current page afterwards. */
export function ssoHref(pathname: string, search: string): string {
  let path = pathname.startsWith(ROOT) ? pathname.slice(ROOT.length) : pathname;
  if (!path.startsWith('/')) path = '/' + path;
  const q = new URLSearchParams(search);
  q.delete('error');
  const rest = q.toString();
  return `${ROOT}/api/oidc/login?return_to=${encodeURIComponent(path + (rest ? '?' + rest : ''))}`;
}

const messages: Record<string, string> = {
  domain: 'This account may not sign in to this Folio. Use an account with an allowed e-mail address.',
  state: 'The sign-in expired or was started in another tab. Try again.',
  provider: 'The sign-in provider did not sign you in. Try again.',
  exchange: 'Folio could not finish the sign-in with the provider. Try again.',
  token: 'Folio could not verify the answer of the sign-in provider. Try again.',
  sso_unavailable: 'SSO is unavailable right now. Try again in a minute.',
  rate_limited: 'Too many sign-in attempts. Wait a minute, then try again.',
};

/** ssoError is the message for a sign-in error code ("" for none). */
export function ssoError(code: string): string {
  if (!code) return '';
  return messages[code] ?? 'Signing in did not work. Try again.';
}

/** methodsText describes the configured sign-in methods (Settings). */
export function methodsText(m: SignInMethods | undefined): string {
  if (!m) return '…';
  if (m.password && m.oidc) return 'Password and SSO.';
  if (m.oidc) return 'SSO only.';
  return 'Password only. Set FOLIO_OIDC_ISSUER on the server to add SSO.';
}
