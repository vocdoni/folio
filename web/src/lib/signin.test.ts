// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { methodsText, ssoError, ssoHref } from './signin';

describe('ssoHref', () => {
  it('returns to the current page below /ui', () => {
    expect(ssoHref('/ui/', '')).toBe('/ui/api/oidc/login?return_to=%2F');
    expect(ssoHref('/ui', '')).toBe('/ui/api/oidc/login?return_to=%2F');
    expect(ssoHref('/ui/documents/0199a0', '?status=draft')).toBe('/ui/api/oidc/login?return_to=%2Fdocuments%2F0199a0%3Fstatus%3Ddraft');
  });
  it('drops a previous error code', () => {
    expect(ssoHref('/ui/', '?error=domain')).toBe('/ui/api/oidc/login?return_to=%2F');
    expect(ssoHref('/ui/activity', '?error=state&actor=ui')).toBe('/ui/api/oidc/login?return_to=%2Factivity%3Factor%3Dui');
  });
});

describe('ssoError', () => {
  it('explains each callback code', () => {
    expect(ssoError('')).toBe('');
    expect(ssoError('domain')).toContain('may not sign in');
    expect(ssoError('sso_unavailable')).toBe('SSO is unavailable right now. Try again in a minute.');
    expect(ssoError('rate_limited')).toContain('Too many');
    expect(ssoError('<script>')).toBe('Signing in did not work. Try again.');
  });
});

describe('methodsText', () => {
  it('names the configured methods', () => {
    expect(methodsText(undefined)).toBe('…');
    expect(methodsText({ password: true, oidc: { name: 'Sign in with SSO' } })).toBe('Password and SSO.');
    expect(methodsText({ password: false, oidc: { name: 'Sign in with SSO' } })).toBe('SSO only.');
    expect(methodsText({ password: true, oidc: null })).toContain('Password only');
  });
});
