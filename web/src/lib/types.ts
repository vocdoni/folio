// SPDX-License-Identifier: AGPL-3.0-only
// Types of the /ui/api JSON API (mirrors internal/webui).

export interface Finding {
  code: string;
  severity: 'error' | 'warning';
  message: string;
  path?: string;
  page?: number;
  hint?: string;
}

export interface Session {
  authenticated: boolean;
  expires_at?: string;
  ai?: boolean;
  version?: string;
}

export interface Stats {
  documents: number;
  trashed: number;
  templates: number;
  brands: number;
  assets: number;
  renders_today: number;
  calls_24h: number;
  errors_24h: number;
  error_rate_24h: number;
  storage_bytes: number;
  reviewer: boolean;
  ai: boolean;
  sandbox_level: string;
  version: string;
}

export interface ActivityDetail {
  revision?: number;
  version?: number;
  pages?: number;
  bytes?: number;
  formats?: string[];
  unchanged?: boolean;
  purge?: boolean;
  archived?: boolean;
  findings_errors?: number;
  findings_warnings?: number;
}

export interface ActivityRow {
  id: number;
  ts: string;
  source: 'mcp' | 'ui';
  actor: string;
  action: string;
  target_kind?: string;
  target_id?: string;
  outcome: 'ok' | 'error';
  error_code?: string;
  duration_ms: number;
  detail: ActivityDetail;
}

export interface ActivityPage {
  items: ActivityRow[];
  next_before?: number;
  actors: string[];
  actions: string[];
}

export type DocStatus = 'draft' | 'published' | 'trashed';

export interface DocumentSummary {
  id: string;
  title: string;
  template_id: string;
  client: string;
  status: DocStatus;
  latest_revision: number;
  published_revision?: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  preview_url?: string;
}

export interface Ref {
  id: string;
  version: number;
}

export interface FileOut {
  kind: 'pdf' | 'docx' | 'preview';
  name: string;
  mime: string;
  size: number;
  page?: number;
  url: string;
}

export interface Revision {
  revision: number;
  parent_revision?: number;
  template: Ref;
  brand: Ref;
  warnings?: Finding[];
  review?: { findings?: { page: number; severity: string; category: string; message: string }[] };
  created_by: string;
  created_at: string;
}

export interface DocumentDetail {
  id: string;
  title: string;
  template_id: string;
  client: string;
  status: DocStatus;
  latest_revision: number;
  published_revision?: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  revision: Revision;
  revisions: Revision[];
  files: FileOut[];
  page_count: number;
  page_urls: string[];
}

export interface Source {
  template_id: string;
  markdown: string;
  metadata: Record<string, unknown>;
  data: Record<string, unknown>;
  formats: string[];
  revision: number;
}

export interface RenderResult {
  document_id: string;
  revision: number;
  unchanged: boolean;
  status: DocStatus;
  template: Ref;
  brand: Ref;
  files: FileOut[];
  warnings: Finding[];
}

export interface Section {
  title: string;
  required?: boolean;
  aliases?: string[];
  hint?: string;
  component?: string;
}

export interface TemplateSpec {
  spec_version?: number;
  id: string;
  name: string;
  description: string;
  category: string;
  brand_id: string;
  languages: string[];
  layout: {
    base: string;
    typst: string | null;
    options?: { cover?: boolean; toc?: boolean; page_size?: 'a4' | 'letter'; heading_numbering?: boolean };
  };
  metadata_schema?: JSONSchema;
  data_schema?: JSONSchema;
  sections: Section[];
  components: string[];
  strict: boolean;
  body_skeleton: string;
  sample: { markdown: string; metadata: Record<string, unknown>; data?: Record<string, unknown> };
  labels?: Record<string, Record<string, string>>;
}

export interface TemplateSummary {
  id: string;
  name: string;
  description: string;
  category: string;
  brand_id: string;
  languages: string[];
  layout: string;
  sections: { title: string; required?: boolean; component?: string }[];
  version: number;
  archived: boolean;
}

export interface VersionInfo {
  version: number;
  note: string;
  created_by: string;
  created_at: string;
}

export interface Item<S> {
  id: string;
  version: number;
  latest_version: number;
  archived: boolean;
  spec: S;
  versions: VersionInfo[];
  core_metadata_schema?: JSONSchema;
}

export interface BrandSpec {
  spec_version?: number;
  id: string;
  name: string;
  colors: { primary: string; secondary: string; accent: string; text: string; muted: string; background: string };
  fonts: { heading: string; body: string; mono: string };
  logo?: { asset_id: string; width_mm: number };
  header: { text: string; show_logo: boolean };
  footer: { text: string; legal: string; page_numbers: boolean };
  docx: { font_fallbacks?: Record<string, string> };
}

export interface BrandSummary {
  id: string;
  version: number;
  archived: boolean;
  spec: BrandSpec;
}

export interface Asset {
  asset_id: string;
  name: string;
  description?: string;
  mime: string;
  sha256: string;
  size: number;
  width: number;
  height: number;
  created_by: string;
  created_at: string;
  url: string;
}

export interface Capabilities {
  version: string;
  languages: string[];
  layouts: { name: string; options_schema: JSONSchema }[];
  components: { name: string; schema: JSONSchema }[];
  fonts: { family: string; regular?: string; bold?: string }[];
  label_keys: string[];
  core_metadata_schema: JSONSchema;
  limits: {
    max_markdown_bytes: number;
    max_asset_bytes: number;
    max_request_bytes: number;
    max_pages: number;
    preview_pages_stored: number;
    trash_retention_days: number;
  };
  reviewer: boolean;
  sandbox_level: string;
  public_url: string;
  mcp_url: string;
}

export interface PreviewResult {
  ok: boolean;
  message?: string;
  findings: Finding[];
  preview_id?: string;
  pages: number;
  page_urls: string[];
  cached: boolean;
}

export interface AIStatus {
  enabled: boolean;
  model?: string;
  vision: boolean;
  daily_limit?: number;
  daily_remaining?: number;
  daily_resets_at?: string;
  session_limit?: number;
  session_remaining?: number;
}

export interface Job<R> {
  job_id: string;
  kind: string;
  state: 'queued' | 'running' | 'done' | 'error';
  step?: string;
  started_at: string;
  result?: R;
  error?: { code: string; message: string };
}

export interface Plan {
  template_id: string;
  metadata: Record<string, unknown>;
  data: Record<string, unknown>;
  warnings: string[];
  inferred: string[];
}

export interface DraftResult {
  spec: TemplateSpec;
  ok: boolean;
  findings: Finding[];
  repaired: boolean;
  preview_id?: string;
  page_urls: string[];
  pages: number;
}

export interface BrandSuggestion {
  brand: BrandSpec;
  findings: Finding[];
  swatches?: { hex: string; share: number }[];
  evidence?: {
    url: string;
    title?: string;
    theme_color?: string;
    colors: { hex: string; count: number }[];
    semantic?: Record<string, string>;
    fonts?: { family: string; source: string; closest?: string }[];
    images: string[];
  };
  logo_index: number;
}

/** The JSON Schema shapes the form generator reads (a subset). */
export interface JSONSchema {
  type?: string | string[];
  title?: string;
  description?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  additionalProperties?: boolean | JSONSchema;
  items?: JSONSchema;
  enum?: unknown[];
  format?: string;
  pattern?: string;
  maxLength?: number;
  minLength?: number;
  minItems?: number;
  maxItems?: number;
  $ref?: string;
  allOf?: JSONSchema[];
  [k: string]: unknown;
}

/** GET /connection: what an agent needs to connect (tokens empty when no token is needed). */
export interface Connection {
  mcp_url: string;
  tokens: { name: string; token: string }[];
  skill_url: string;
}
