// SPDX-License-Identifier: AGPL-3.0-only
// End-to-end tests against a running Folio (FOLIO_URL) whose OpenRouter base
// URL points at e2e/stub-openrouter.mjs. CI starts both as containers.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: process.env.FOLIO_URL ?? 'http://127.0.0.1:18080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'setup', testMatch: /auth\.setup\.ts/ },
    {
      name: 'desktop',
      dependencies: ['setup'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 }, storageState: 'test-results/.auth/state.json' },
    },
    {
      name: 'mobile',
      dependencies: ['setup'],
      grep: /@mobile/,
      use: { ...devices['Pixel 7'], storageState: 'test-results/.auth/state.json' },
    },
  ],
});
