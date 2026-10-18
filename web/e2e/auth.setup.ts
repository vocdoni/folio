// SPDX-License-Identifier: AGPL-3.0-only
// Signs in once and stores the session cookie for the other tests.
import { expect, test as setup } from '@playwright/test';

export const STATE = 'test-results/.auth/state.json';

setup('sign in', async ({ page }) => {
  await page.goto('/ui/');
  await page.getByLabel('Password').fill(process.env.FOLIO_UI_PASSWORD ?? '');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  await page.context().storageState({ path: STATE });
});
