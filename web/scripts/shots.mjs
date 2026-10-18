// SPDX-License-Identifier: AGPL-3.0-only
// Screenshots of every screen at desktop and mobile widths (design review).
//   FOLIO_URL=http://127.0.0.1:18080 FOLIO_UI_PASSWORD=… node scripts/shots.mjs <out-dir>
import { chromium } from '@playwright/test';

const base = process.env.FOLIO_URL ?? 'http://127.0.0.1:18080';
const out = process.argv[2] ?? 'shots';
const password = process.env.FOLIO_UI_PASSWORD ?? '';
const screens = [
  ['overview', '/ui/'],
  ['activity', '/ui/activity'],
  ['documents', '/ui/documents'],
  ['create', '/ui/create'],
  ['templates', '/ui/templates'],
  ['studio', '/ui/templates/proposal'],
  ['brands', '/ui/brands'],
  ['brand', '/ui/brands/example'],
  ['assets', '/ui/assets'],
  ['settings', '/ui/settings'],
];
const browser = await chromium.launch();
for (const [label, width, height] of [['desktop', 1440, 900], ['mobile', 390, 844]]) {
  const ctx = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: label === 'mobile' ? 2 : 1 });
  const page = await ctx.newPage();
  const problems = [];
  page.on('console', (m) => m.type() === 'error' && problems.push(`console: ${m.text()}`));
  page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.error(`CSP ${e.violatedDirective} ${e.blockedURI}`));
  });
  await page.goto(base + '/ui/');
  await page.screenshot({ path: `${out}/${label}-signin.png`, fullPage: true });
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('heading', { name: 'Overview' }).waitFor();
  for (const [name, path] of screens) {
    await page.goto(base + path);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(name === 'studio' ? 4000 : 800);
    await page.screenshot({ path: `${out}/${label}-${name}.png`, fullPage: name !== 'studio' });
  }
  await page.goto(base + '/ui/documents');
  await page.waitForLoadState('networkidle');
  await page.locator('.card').first().click();
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${out}/${label}-document-drawer.png` });
  console.log(label, problems.length ? problems.join('\n') : 'no console errors or CSP violations');
  await ctx.close();
}
await browser.close();
