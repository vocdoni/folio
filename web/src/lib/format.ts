// SPDX-License-Identifier: AGPL-3.0-only
// Display helpers: dates, sizes and the plain-language names of actions.

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function percent(r: number): string {
  if (r === 0) return '0%';
  const p = r * 100;
  return `${p < 1 ? p.toFixed(1) : Math.round(p)}%`;
}

const dateFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
const timeFmt = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const dayTimeFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

export function date(iso: string): string {
  return dateFmt.format(new Date(iso));
}

export function clock(iso: string | Date): string {
  return timeFmt.format(typeof iso === 'string' ? new Date(iso) : iso);
}

/** when is "14:32" today, "18 Oct, 14:32" otherwise. */
export function when(iso: string, now = new Date()): string {
  const d = new Date(iso);
  return d.toDateString() === now.toDateString() ? timeFmt.format(d) : dayTimeFmt.format(d);
}

/** ago is a short relative time ("just now", "5 min ago", "3 h ago", or the date). */
export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return date(iso);
}

/** minuteKey groups activity rows by local minute. */
export function minuteKey(iso: string): string {
  const d = new Date(iso);
  d.setSeconds(0, 0);
  return d.toISOString();
}

const actionNames: Record<string, string> = {
  render_document: 'Rendered a document',
  validate_document: 'Checked a document',
  get_document: 'Opened a document',
  list_documents: 'Listed documents',
  publish_document: 'Published a document',
  delete_document: 'Deleted a document',
  restore_document: 'Restored a document',
  review_document: 'Reviewed a document’s layout',
  get_guide: 'Read a guide',
  get_capabilities: 'Read the capabilities',
  list_templates: 'Listed templates',
  get_template: 'Opened a template',
  create_template: 'Created a template',
  update_template: 'Saved a template version',
  archive_template: 'Archived or restored a template',
  preview_template: 'Previewed a template',
  list_brands: 'Listed brands',
  get_brand: 'Opened a brand',
  create_brand: 'Created a brand',
  update_brand: 'Saved a brand version',
  archive_brand: 'Archived or restored a brand',
  upload_asset: 'Uploaded an image',
  list_assets: 'Listed images',
  delete_asset: 'Deleted an image',
  'ui.login': 'Signed in',
  'ui.logout': 'Signed out',
  'ui.connection.reveal': 'Viewed the connection details',
  'ui.render': 'Rendered a document',
  'ui.document.publish': 'Published a document',
  'ui.document.delete': 'Deleted a document',
  'ui.document.restore': 'Restored a document',
  'ui.template.create': 'Created a template',
  'ui.template.update': 'Saved a template version',
  'ui.template.archive': 'Archived or restored a template',
  'ui.brand.create': 'Created a brand',
  'ui.brand.update': 'Saved a brand version',
  'ui.brand.archive': 'Archived or restored a brand',
  'ui.asset.upload': 'Uploaded an image',
  'ui.asset.delete': 'Deleted an image',
  'ui.ai.document-plan': 'Asked the assistant to fill in details',
  'ui.ai.template-draft': 'Asked the assistant for a template',
  'ui.ai.brand-suggest': 'Asked the assistant for a brand',
  'ui.ai.brand-logo': 'Saved a logo from a website',
};

/** actionName is the plain-language label of a tool or UI action. */
export function actionName(action: string): string {
  return actionNames[action] ?? action;
}

/** errorName explains an error code in a few words. */
export function errorName(code: string | undefined): string {
  switch (code) {
    case 'validation_failed':
      return 'invalid input';
    case 'not_found':
      return 'not found';
    case 'conflict':
      return 'conflict';
    case 'busy':
      return 'queue full';
    case 'timeout':
      return 'took too long';
    case 'render_failed':
      return 'render failed';
    case 'unauthorized':
      return 'wrong password';
    case 'rate_limited':
      return 'too many attempts';
    default:
      return code ? code.replaceAll('_', ' ') : 'error';
  }
}

/** pluralize("document", n) → "1 document" / "3 documents". */
export function count(n: number, word: string, plural = word + 's'): string {
  return `${n.toLocaleString()} ${n === 1 ? word : plural}`;
}
