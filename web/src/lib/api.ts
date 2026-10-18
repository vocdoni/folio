// SPDX-License-Identifier: AGPL-3.0-only
// The /ui/api client: JSON in and out, the CSRF header on every change,
// errors as ApiError with the server's code, message and findings.

import type { Asset, Finding } from './types';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly details: Finding[] = [],
    readonly retryAfter = 0,
  ) {
    super(message);
  }
}

type Listener = () => void;
const unauthorized = new Set<Listener>();

/** onUnauthorized registers a callback for 401 answers (session ended). */
export function onUnauthorized(fn: Listener): () => void {
  unauthorized.add(fn);
  return () => unauthorized.delete(fn);
}

export const BASE = '/ui/api';

export interface RequestOptions {
  signal?: AbortSignal;
  /** Do not treat 401 as "signed out" (the login call). */
  quiet401?: boolean;
}

export async function request<T>(method: string, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  if (method !== 'GET') headers['X-Folio-UI'] = '1';
  let res: Response;
  try {
    res = await fetch(BASE + path, { method, headers, body: payload, credentials: 'same-origin', signal: opts.signal });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError(0, 'offline', 'Folio did not answer. Check your connection and try again.');
  }
  const text = await res.text();
  let json: unknown = undefined;
  try {
    json = text ? JSON.parse(text) : undefined;
  } catch {
    json = undefined;
  }
  if (!res.ok) {
    const err = (json as { error?: { code?: string; message?: string; details?: Finding[] } } | undefined)?.error;
    if (res.status === 401 && !opts.quiet401) unauthorized.forEach((fn) => fn());
    throw new ApiError(
      res.status,
      err?.code ?? 'http_' + res.status,
      err?.message ?? `The server answered ${res.status}.`,
      err?.details ?? [],
      Number(res.headers.get('Retry-After') ?? 0),
    );
  }
  return json as T;
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, undefined, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', path, body ?? {}, opts),
  put: <T>(path: string, body: unknown, opts?: RequestOptions) => request<T>('PUT', path, body, opts),
  patch: <T>(path: string, body: unknown, opts?: RequestOptions) => request<T>('PATCH', path, body, opts),
  del: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, undefined, opts),
};

/** updateAsset renames an image and replaces its description. */
export function updateAsset(id: string, name: string, description: string, opts?: RequestOptions): Promise<Asset> {
  return api.patch<Asset>(`/assets/${encodeURIComponent(id)}`, { name, description }, opts);
}

/** query builds "?a=1&b=2" from the non-empty values. */
export function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '' || v === false) continue;
    q.set(k, String(v));
  }
  const s = q.toString();
  return s ? '?' + s : '';
}

/** fileToBase64 reads a File as standard base64 (no data: prefix). */
export async function fileToBase64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let bin = '';
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(bin);
}

/** uuid returns a random v4 UUID (idempotency keys). */
export function uuid(): string {
  return crypto.randomUUID();
}
