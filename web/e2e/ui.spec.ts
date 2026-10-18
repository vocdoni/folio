// SPDX-License-Identifier: AGPL-3.0-only
// The web UI end to end (stub AI): sign in, live activity, documents and
// downloads, create from Markdown, the template studio, brands from a logo;
// on every screen: axe-core (no serious or critical violations), no console
// errors, no CSP violations, no request leaving the Folio origin.
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const password = process.env.FOLIO_UI_PASSWORD ?? '';
const token = process.env.FOLIO_E2E_TOKEN ?? '';
const fixture = (name: string) => fileURLToPath(new URL(`./fixtures/${name}`, import.meta.url));

interface Watch {
  problems: string[];
}

async function watch(page: Page): Promise<Watch> {
  const w: Watch = { problems: [] };
  const origin = new URL(test.info().project.use.baseURL!).origin;
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`));
  });
  page.on('console', (m) => {
    if (m.type() === 'error') w.problems.push(`console: ${m.text()}`);
  });
  page.on('pageerror', (e) => w.problems.push(`page error: ${e.message}`));
  page.on('request', (r) => {
    const u = r.url();
    if (!u.startsWith('data:') && !u.startsWith('blob:') && new URL(u).origin !== origin) w.problems.push(`external request: ${u}`);
  });
  return w;
}

// Every test except the sign-in one starts signed in (auth.setup.ts signs in
// once: the login limit is five attempts a minute per address).
async function signIn(page: Page) {
  await page.goto('/ui/');
  await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
}

async function axe(page: Page, screen: string) {
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']).analyze();
  const bad = r.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  expect(bad.map((v) => `${screen}: ${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([]);
}

async function mcp(page: Page, name: string, args: unknown) {
  const res = await page.request.post('/mcp', {
    headers: { Authorization: `Bearer ${token}`, Accept: 'application/json, text/event-stream' },
    data: { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } },
  });
  expect(res.ok()).toBeTruthy();
  const text = await res.text();
  const data = text.split('\n').find((l) => l.startsWith('data: '));
  return JSON.parse(data ? data.slice(6) : text);
}

test.describe('signed out', () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test('sign in is required and wrong passwords are refused', async ({ page }) => {
  const w = await watch(page);
  expect((await page.request.get('/ui/api/stats')).status()).toBe(401);
  await page.goto('/ui/documents');
  await page.getByLabel('Password').fill('not the password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('not right');
  await axe(page, 'sign in');
  w.problems = w.problems.filter((p) => !p.includes('401')); // the failed login answers 401 by design
  expect(w.problems).toEqual([]);
  });
});

test('activity shows agent calls live @mobile', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/activity');
  await expect(page.locator('.masthead .live')).toHaveText('Live');
  await mcp(page, 'get_template', { id: 'quote' });
  await expect(page.locator('.timeline').getByText('Opened a template').first()).toBeVisible();
  await expect(page.locator('.timeline').getByRole('link', { name: 'quote' }).first()).toBeVisible();
  await axe(page, 'activity');
  await page.goto('/ui/');
  await axe(page, 'overview');
  expect(w.problems).toEqual([]);
});

test('open a document, page through it and download its PDF', async ({ page }) => {
  const w = await watch(page);
  const t = await mcp(page, 'get_template', { id: 'proposal' });
  const spec = t.result.structuredContent.template;
  await mcp(page, 'render_document', {
    template_id: 'proposal',
    markdown: spec.sample.markdown,
    metadata: { ...spec.sample.metadata, title: 'E2E proposal' },
    data: spec.sample.data,
    idempotency_key: 'e2e-ui-proposal',
  });
  await signIn(page);
  await page.goto('/ui/documents');
  await page.getByLabel('Search titles and clients').fill('E2E proposal');
  await page.getByRole('link', { name: /E2E proposal/ }).first().click();
  const drawer = page.getByRole('dialog', { name: 'E2E proposal' });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText(/Page 1 of \d+/)).toBeVisible();
  await page.keyboard.press('ArrowRight');
  await expect(drawer.getByText(/Page 2 of/)).toBeVisible();
  await expect(drawer.getByRole('img', { name: /Page 2/ })).toBeVisible();
  await axe(page, 'document');
  const download = page.waitForEvent('download');
  await drawer.getByRole('link', { name: /Download PDF/ }).click();
  const file = await (await download).path();
  expect(readFileSync(file).subarray(0, 5).toString()).toBe('%PDF-');
  await drawer.getByRole('button', { name: 'Close' }).click();
  await expect(page).toHaveURL(/\/ui\/documents/);
  expect(w.problems).toEqual([]);
});

test('create a document from Markdown with the assistant filling in the details', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/create');
  await page.locator('input[type=file]').first().setInputFiles(fixture('quote.md'));
  await expect(page.locator('#md')).toHaveValue(/Northwind Traders/);
  // Byte counter is English-formatted as a size, never locale digits.
  await expect(page.locator('#md-help')).toHaveText(/^\d+(\.\d)? (B|KB) of 1\.0 MB$/);
  await page.getByRole('button', { name: 'Fill in from my text' }).click();
  await expect(page.getByLabel('Template')).toHaveValue('quote', { timeout: 30_000 });
  await expect(page.getByLabel('Client')).toHaveValue('Northwind Traders');
  // Values the text does not contain are flagged for checking.
  await expect(page.locator('.inferred').first()).toBeVisible();
  await expect(page.getByText('The assistant was unsure about')).toBeVisible();
  await axe(page, 'create');
  await page.getByRole('button', { name: 'Render', exact: true }).click();
  await expect(page.getByRole('heading', { name: /Revision 1 is ready/ })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByRole('link', { name: /Download PDF/ })).toBeVisible();
  await page.getByRole('link', { name: 'Open in documents' }).click();
  await expect(page.getByRole('dialog', { name: 'Lisbon showroom screens' })).toBeVisible();
  expect(w.problems).toEqual([]);
});

test('edit a template in the studio with a live proof and save a version', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/templates');
  await axe(page, 'templates');
  await page.getByRole('link', { name: /Technical report/ }).click();
  await expect(page.getByText(/Proof updated at/)).toBeVisible({ timeout: 60_000 });
  // No stray space before the comma: the whole sentence is one text run.
  const line = page.locator('.status span:not(.dot)').filter({ hasText: /Proof updated at/ });
  expect(await line.evaluate((el) => el.textContent)).toMatch(/^Proof updated at \d{1,2}:\d{2}, \d+ pages?$/);
  expect(await line.evaluate((el) => (el as HTMLElement).innerText)).not.toMatch(/\d , /);
  await axe(page, 'studio');
  await page.getByRole('button', { name: 'Layout' }).click();
  const rerender = page.waitForResponse((r) => r.url().endsWith('/ui/api/templates/preview') && r.ok(), { timeout: 60_000 });
  await page.getByLabel('Table of contents').click(); // toggles either way
  expect((await (await rerender).json()).ok).toBe(true);
  await expect(page.getByText(/Proof updated at/)).toBeVisible({ timeout: 60_000 });
  await page.getByPlaceholder('What changed (optional)').fill('Add a table of contents');
  await page.getByRole('button', { name: 'Save as new version' }).click();
  await expect(page.getByRole('status').getByText(/Saved version \d+ of Technical report/)).toBeVisible({ timeout: 60_000 });
  expect(w.problems).toEqual([]);
});

test('draft a template with the assistant (repair round) and open it unsaved', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/templates');
  await page.getByRole('button', { name: 'Draft with the assistant' }).click();
  await page.getByLabel('What documents is it for?').fill('A short service agreement with fees and signatures.');
  await page.getByRole('button', { name: 'Draft the template' }).click();
  await expect(page).toHaveURL(/\/ui\/templates\/new/, { timeout: 60_000 });
  await expect(page.getByText(/Drafted by the assistant \(after one correction\)/)).toBeVisible();
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Service agreement');
  await expect(page.getByText(/Proof updated at/)).toBeVisible({ timeout: 60_000 });
  expect(w.problems).toEqual([]);
});

test('create a brand from a logo with the assistant @mobile', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/assets');
  await page.locator('input[type=file]').setInputFiles(fixture('northwind-logo.png'));
  await expect(page.getByText('Uploaded northwind-logo.png.')).toBeVisible();
  await axe(page, 'images');
  await page.goto('/ui/brands/new');
  await page.getByRole('button', { name: 'Suggest with the assistant' }).click();
  await page.locator('#ai-asset').selectOption({ label: 'northwind-logo.png' });
  await page.getByRole('button', { name: 'Suggest', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Proposal: Northwind Traders' })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByText('Colours found in the logo')).toContainText('#0B5D4B');
  await page.getByRole('button', { name: 'Apply to the form' }).click();
  await expect(page.locator('#c-primary-hex')).toHaveValue('#0B5D4B');
  await page.locator('#b-id').fill(`northwind-${test.info().project.name}`);
  await axe(page, 'brand editor');
  await page.getByRole('button', { name: 'Create brand' }).click();
  await expect(page.getByRole('status').getByText(/Saved version 1 of Northwind Traders/)).toBeVisible();
  await page.goto('/ui/brands');
  await expect(page.getByRole('link', { name: /Northwind Traders/ }).first()).toBeVisible();
  await axe(page, 'brands');
  await page.goto('/ui/settings');
  await expect(page.getByLabel('MCP address')).toContainText('/mcp');
  await axe(page, 'settings');
  expect(w.problems).toEqual([]);
});

test('the main flows work from the keyboard', async ({ page }) => {
  await signIn(page);
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused();
  await page.keyboard.press('Enter');
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press('Tab');
    if (await page.getByRole('link', { name: 'Documents', exact: true }).evaluate((el) => el === document.activeElement)) break;
  }
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: 'Documents', level: 1 })).toBeVisible();
  await expect(page.locator('.card').first()).toBeVisible();
  await page.getByLabel('Template').focus();
  await page.keyboard.press('Tab');
  await expect(page.locator('.card').first()).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
});

test('the documents status filter shows its whole label on a phone @mobile', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  await page.goto('/ui/documents');
  const select = page.locator('#doc-status');
  await expect(select).toBeVisible();
  const fits = await select.evaluate((el: HTMLSelectElement) => {
    const cs = getComputedStyle(el);
    const ctx = document.createElement('canvas').getContext('2d')!;
    ctx.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
    const widest = Math.max(...Array.from(el.options).map((o) => ctx.measureText(o.text).width));
    const room = el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    return { widest, room };
  });
  expect(fits.widest).toBeLessThanOrEqual(fits.room);
  expect(w.problems).toEqual([]);
});
