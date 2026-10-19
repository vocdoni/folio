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

test('a document rendered with an inline template shows it and saves it as a template', async ({ page }) => {
  const w = await watch(page);
  const r = await mcp(page, 'render_document', {
    template: { layout: { base: 'letter' }, sections: [{ title: 'Greeting', required: true }] },
    brand_id: 'example',
    markdown: '# Greeting\n\nHello from an inline template.',
    metadata: { title: 'E2E inline letter', date: '2026-10-19', language: 'en' },
    idempotency_key: 'e2e-ui-inline',
  });
  expect(r.result.structuredContent.template.inline).toBe(true);
  await signIn(page);
  // Revising keeps the inline template (no picker).
  await page.goto('/ui/create?from=' + r.result.structuredContent.document_id);
  await expect(page.getByText('Inline template', { exact: true })).toBeVisible();
  await page.locator('#md').fill(`# Greeting\n\nRevised in the UI at ${Date.now()}.`);
  await page.getByRole('button', { name: 'Render a new revision' }).click();
  await expect(page.getByRole('heading', { name: /Revision \d+ is ready/ })).toBeVisible({ timeout: 60_000 });
  await page.goto('/ui/documents');
  await page.locator('#doc-template').selectOption('inline');
  await page.getByRole('link', { name: /E2E inline letter/ }).first().click();
  const drawer = page.getByRole('dialog', { name: 'E2E inline letter' });
  await expect(drawer.getByText(/inline template, revision \d+ of \d+/)).toBeVisible();
  await axe(page, 'inline document');
  await drawer.getByRole('button', { name: 'Save as template' }).click();
  const save = page.getByRole('dialog', { name: 'Save as a template' });
  await save.getByLabel('Template id').fill(`e2e-inline-${Date.now().toString(36)}`); // unique: reruns reuse the document
  await save.getByLabel('Name').fill('E2E inline letter');
  await axe(page, 'save as template');
  await save.getByRole('button', { name: 'Save template' }).click();
  await expect(page.getByText('Saved as the template “E2E inline letter”.')).toBeVisible({ timeout: 60_000 });
  await page.goto('/ui/templates');
  await expect(page.getByText('E2E inline letter').first()).toBeVisible();
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
  const naming = page.getByRole('dialog', { name: 'Name your images' });
  await expect(naming).toBeVisible();
  await expect(naming.getByLabel('Name', { exact: true })).toHaveValue('northwind-logo');
  await naming.getByRole('button', { name: 'Upload image' }).click();
  await expect(page.getByText('Uploaded northwind-logo.')).toBeVisible();
  await axe(page, 'images');
  await page.goto('/ui/brands/new');
  await page.getByRole('button', { name: 'Suggest with the assistant' }).click();
  await page.locator('#ai-asset').selectOption({ label: 'northwind-logo' });
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
  await expect(page.getByLabel('Claude Code install command', { exact: true })).toContainText('folio --marketplace');
  await axe(page, 'settings');
  expect(w.problems).toEqual([]);
});

test('name an image at upload and rename it later @mobile', async ({ page }) => {
  const w = await watch(page);
  await signIn(page);
  const unique = test.info().project.name;
  const first = `Quarterly mark ${unique}`;
  const second = `Annual mark ${unique}`;
  await page.goto('/ui/assets');
  const picker = page.locator('input[type=file]');

  // Escape discards the pick: nothing is uploaded.
  await picker.setInputFiles(fixture('northwind-logo.png'));
  const naming = page.getByRole('dialog', { name: 'Name your images' });
  await expect(naming).toBeVisible();
  await expect(naming.getByLabel('Name', { exact: true })).toBeFocused();
  await axe(page, 'name your images');
  await page.keyboard.press('Escape');
  await expect(naming).toBeHidden();
  await expect(page.getByText('Uploaded', { exact: false })).toHaveCount(0);

  // An empty name blocks the upload; a custom name and description are kept.
  await picker.setInputFiles(fixture('northwind-logo.png'));
  await expect(naming).toBeVisible();
  const name = naming.getByLabel('Name', { exact: true });
  await name.fill('');
  await expect(naming.getByText('Give the image a name.')).toBeVisible();
  await expect(naming.getByRole('button', { name: 'Upload image' })).toBeDisabled();
  await name.fill(first);
  await naming.getByLabel(/Describe the image for people who cannot see it/).fill('A green mark on white');
  await naming.getByRole('button', { name: 'Upload image' }).click();
  await expect(page.getByText(`Uploaded ${first}.`)).toBeVisible();
  const tile = page.locator('.tile', { hasText: first });
  await expect(tile.getByRole('img')).toHaveAttribute('alt', 'A green mark on white');

  // Rename and re-describe it.
  await tile.getByRole('button', { name: `Edit ${first}` }).click();
  const edit = page.getByRole('dialog', { name: 'Edit image' });
  await expect(edit.getByLabel('Name', { exact: true })).toHaveValue(first);
  await edit.getByLabel('Name', { exact: true }).fill(second);
  await edit.getByLabel(/Describe the image for people who cannot see it/).fill('The annual report mark');
  await axe(page, 'edit image');
  await edit.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Saved.')).toBeVisible();
  const renamed = page.locator('.tile', { hasText: second });
  await expect(renamed).toBeVisible();
  await expect(renamed.getByRole('img')).toHaveAttribute('alt', 'The annual report mark');
  await expect(page.locator('.tile', { hasText: first })).toHaveCount(0);

  // The new name is stored: it survives a reload and shows in the API.
  await page.reload();
  await expect(page.locator('.tile', { hasText: second })).toBeVisible();
  const list = await (await page.request.get('/ui/api/assets?query=' + encodeURIComponent(second))).json();
  expect(list.items.map((a: { name: string }) => a.name)).toContain(second);
  await axe(page, 'images');
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

test('Settings reveals and copies the token and shows user-wide installs @mobile', async ({ page, request }) => {
  const w = await watch(page);
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await signIn(page);
  await page.goto('/ui/settings');
  await expect(page.getByRole('heading', { name: 'Connect your agents', level: 2 })).toBeVisible();
  const clipboard = () => page.evaluate(() => navigator.clipboard.readText());

  // Claude Code is the plugin only, user scope, and says what it installs.
  const plugin = page.getByLabel('Claude Code install command', { exact: true });
  await expect(plugin).toContainText('claude plugin install folio --marketplace vocdoni/folio --scope user');
  await expect(plugin).toContainText('--config folio_url=');
  await expect(page.getByText('sets up the MCP connection and the Folio skill together')).toBeVisible();
  await expect(page.locator('body')).not.toContainText('claude mcp add');

  // Hidden by default: the token is nowhere on the page, but Copy still copies it.
  const cursor = page.getByLabel('Cursor configuration', { exact: true });
  await expect(page.locator('body')).not.toContainText(token);
  await page.getByRole('button', { name: 'Copy token' }).click();
  expect(await clipboard()).toBe(token);

  // Show fills in the snippets; Hide masks them again.
  await page.getByRole('button', { name: 'Show token' }).click();
  await expect(page.getByRole('button', { name: 'Show token' })).toHaveAttribute('aria-pressed', 'true');
  await expect(cursor).toContainText(token);
  await expect(plugin).toContainText(`--config folio_token='${token}'`);
  await page.getByRole('button', { name: 'Copy Claude Code install command' }).click();
  expect(await clipboard()).toContain(`--config folio_token='${token}'`);
  await page.getByRole('button', { name: 'Copy Cursor configuration' }).click();
  expect(await clipboard()).toContain(`"Authorization": "Bearer ${token}"`);
  await page.getByRole('button', { name: 'Copy Codex configuration' }).click();
  expect(await clipboard()).toContain(`Bearer ${token}`);
  await page.getByRole('button', { name: 'Show token' }).click();
  await expect(page.getByRole('button', { name: 'Show token' })).toHaveAttribute('aria-pressed', 'false');
  await expect(cursor).not.toContainText(token);

  await expect(page.getByText('Anyone with this token can use Folio as you. Keep it out of shared repositories, and note that pasted commands stay in your shell history.')).toBeVisible();
  await expect(page.getByLabel('Codex skill install command', { exact: true })).toContainText('/skills/folio/SKILL.md -o ~/.codex/skills/folio/SKILL.md');
  await expect(page.getByRole('heading', { name: 'Things to ask your agent', level: 2 })).toBeVisible();
  await axe(page, 'settings connect');

  // The skill is public and is the single file.
  const anon = await request.get('/skills/folio/SKILL.md', { headers: { Cookie: '' } });
  expect(anon.status()).toBe(200);
  expect(anon.headers()['content-type']).toBe('text/markdown; charset=utf-8');
  expect(await anon.text()).toContain('name: folio');
  expect(w.problems).toEqual([]);
});
