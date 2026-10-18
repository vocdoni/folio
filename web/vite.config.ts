// SPDX-License-Identifier: AGPL-3.0-only
import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import type { Plugin } from 'vite';

// FOLIO_URL is the local Folio the dev server proxies the API to (make dev-ui).
const folio = process.env.FOLIO_URL ?? 'http://localhost:8080';

// emptyOutDir wipes internal/webui/dist, including the committed .keep that
// lets Go embed the directory before the UI is built: emit it again.
const keep: Plugin = {
  name: 'folio-keep',
  apply: 'build',
  generateBundle() {
    this.emitFile({ type: 'asset', fileName: '.keep', source: '' });
  },
};

export default defineConfig({
  base: '/ui/',
  plugins: [svelte(), keep],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0, // fonts and images stay files: CSP font-src/img-src 'self'
    target: 'es2023',
  },
  server: {
    port: 5173,
    proxy: {
      '/ui/api': { target: folio, changeOrigin: true, headers: { Origin: folio } },
      '/ui/fonts': { target: folio, changeOrigin: true },
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
