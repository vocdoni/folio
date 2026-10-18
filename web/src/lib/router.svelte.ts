// SPDX-License-Identifier: AGPL-3.0-only
// A small history router under /ui. Links are plain <a href="/ui/...">;
// a document-level click handler turns same-app clicks into navigate().

import { ROOT, match } from './routes';

export { ROOT };

class Router {
  path = $state(location.pathname);
  search = $state(location.search);
  route = $derived(match(this.path));

  constructor() {
    addEventListener('popstate', () => this.sync());
    document.addEventListener('click', (e) => this.onClick(e));
  }

  sync() {
    this.path = location.pathname;
    this.search = location.search;
  }

  /** navigate goes to an app path ("/documents" or "/ui/documents"). */
  navigate(to: string, opts: { replace?: boolean } = {}) {
    const url = to.startsWith(ROOT) ? to : ROOT + (to === '/' ? '/' : to);
    if (opts.replace) history.replaceState(null, '', url);
    else history.pushState(null, '', url);
    this.sync();
    if (!opts.replace) window.scrollTo(0, 0);
  }

  /** param reads a query parameter of the current URL. */
  param(name: string): string {
    return new URLSearchParams(this.search).get(name) ?? '';
  }

  /** setParams updates query parameters without a history entry. */
  setParams(params: Record<string, string>) {
    const q = new URLSearchParams(this.search);
    for (const [k, v] of Object.entries(params)) {
      if (v) q.set(k, v);
      else q.delete(k);
    }
    const s = q.toString();
    history.replaceState(null, '', this.path + (s ? '?' + s : ''));
    this.search = s ? '?' + s : '';
  }

  private onClick(e: MouseEvent) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = (e.target as Element | null)?.closest('a');
    if (!a || a.target || a.hasAttribute('download')) return;
    const url = new URL(a.href, location.href);
    if (url.origin !== location.origin || !url.pathname.startsWith(ROOT + '/') || url.pathname.startsWith(ROOT + '/api/')) return;
    e.preventDefault();
    this.navigate(url.pathname + url.search);
  }
}

export const router = new Router();

/** href builds an app link. */
export function href(path: string): string {
  return ROOT + path;
}
