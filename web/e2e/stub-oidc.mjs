// SPDX-License-Identifier: AGPL-3.0-only
// A stub OpenID Connect provider for the end-to-end tests: discovery, a JWKS
// with an RSA key generated at start, an authorization endpoint that signs
// the browser in at once as STUB_OIDC_EMAIL, and a token endpoint that
// checks the client secret and PKCE and issues an RS256 ID token. It never
// calls a real provider.
//
//   node e2e/stub-oidc.mjs [port]   (default 8282)
//
// STUB_OIDC_ISSUER is the issuer and the base of the endpoints Folio calls
// (token, keys); STUB_OIDC_AUTH_URL is the authorization endpoint the browser
// opens (on the CI Docker network they differ: http://idp:8282 for Folio,
// the published port for the browser). STUB_OIDC_CLIENT_ID,
// STUB_OIDC_CLIENT_SECRET and STUB_OIDC_REDIRECT_URI describe the one client.

import { createServer } from 'node:http';
import { createHash, createSign, generateKeyPairSync, randomBytes } from 'node:crypto';

const port = Number(process.argv[2] ?? process.env.STUB_PORT ?? 8282);
const issuer = process.env.STUB_OIDC_ISSUER ?? `http://127.0.0.1:${port}`;
const authURL = process.env.STUB_OIDC_AUTH_URL ?? `${issuer}/auth`;
const clientId = process.env.STUB_OIDC_CLIENT_ID ?? 'folio';
const clientSecret = process.env.STUB_OIDC_CLIENT_SECRET ?? '';
const redirectURI = process.env.STUB_OIDC_REDIRECT_URI ?? '';
const email = process.env.STUB_OIDC_EMAIL ?? 'e2e.user@example.test';

const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'stub-1', alg: 'RS256', use: 'sig' };
const grants = new Map(); // code → { nonce, challenge }, single use
const b64 = (v) => Buffer.from(v).toString('base64url');

function idToken(nonce) {
  const now = Math.floor(Date.now() / 1000);
  const head = b64(JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: jwk.kid }));
  const body = b64(JSON.stringify({ iss: issuer, sub: 'e2e-user', aud: clientId, iat: now, exp: now + 600, nonce, email, email_verified: true, name: 'E2E User' }));
  return `${head}.${body}.${b64(createSign('RSA-SHA256').update(`${head}.${body}`).sign(privateKey))}`;
}

function json(res, status, v) {
  res.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(v));
}

// clientOf reads the client credentials: HTTP Basic (form-encoded parts) or the form.
function clientOf(req, form) {
  const auth = req.headers.authorization ?? '';
  if (!auth.startsWith('Basic ')) return [form.get('client_id'), form.get('client_secret')];
  const [id, secret = ''] = Buffer.from(auth.slice(6), 'base64').toString().split(':');
  return [decodeURIComponent(id.replaceAll('+', ' ')), decodeURIComponent(secret.replaceAll('+', ' '))];
}

createServer((req, res) => {
  const url = new URL(req.url ?? '/', issuer);
  if (req.method === 'GET' && url.pathname === '/healthz') {
    res.end('ok');
  } else if (req.method === 'GET' && url.pathname === '/.well-known/openid-configuration') {
    json(res, 200, {
      issuer, authorization_endpoint: authURL, token_endpoint: `${issuer}/token`, jwks_uri: `${issuer}/keys`,
      response_types_supported: ['code'], subject_types_supported: ['public'],
      id_token_signing_alg_values_supported: ['RS256'], code_challenge_methods_supported: ['S256'],
    });
  } else if (req.method === 'GET' && url.pathname === '/keys') {
    json(res, 200, { keys: [jwk] });
  } else if (req.method === 'GET' && url.pathname === '/auth') {
    const q = url.searchParams;
    if (q.get('client_id') !== clientId || q.get('redirect_uri') !== redirectURI || q.get('response_type') !== 'code' ||
      q.get('code_challenge_method') !== 'S256' || !q.get('code_challenge') || !q.get('nonce') || !q.get('state')) {
      res.writeHead(400).end('stub: bad authorization request');
      return;
    }
    const code = b64(randomBytes(24));
    grants.set(code, { nonce: q.get('nonce'), challenge: q.get('code_challenge') });
    const to = new URL(redirectURI);
    to.searchParams.set('code', code);
    to.searchParams.set('state', q.get('state'));
    res.writeHead(302, { Location: to.toString() }).end();
  } else if (req.method === 'POST' && url.pathname === '/token') {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      const form = new URLSearchParams(body);
      const [id, secret] = clientOf(req, form);
      const code = form.get('code') ?? '';
      const grant = grants.get(code);
      grants.delete(code);
      const challenge = b64(createHash('sha256').update(form.get('code_verifier') ?? '').digest());
      if (id !== clientId || secret !== clientSecret || form.get('grant_type') !== 'authorization_code' || !grant ||
        form.get('redirect_uri') !== redirectURI || challenge !== grant.challenge) {
        json(res, 400, { error: 'invalid_grant' });
        return;
      }
      json(res, 200, { access_token: b64(randomBytes(16)), token_type: 'Bearer', expires_in: 600, id_token: idToken(grant.nonce) });
    });
  } else {
    res.writeHead(404).end();
  }
}).listen(port, () => console.log(`stub OpenID Connect provider on :${port} (issuer ${issuer})`));
