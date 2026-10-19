// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

// Instructions is sent to clients at initialisation. Limits: at most 1200
// characters, and the first 512 must stand alone (clients may truncate).
const Instructions = `Folio renders branded PDF and editable DOCX documents from Markdown, metadata and typed data, using shared templates and brands. Core loop: list_templates → get_template (sections, schemas, body_skeleton, sample) → upload_asset (only for images) → validate_document → render_document → share the returned download links. Folio never changes your content or figures: send every value exactly as it should appear, totals included. Call get_guide for details.

To revise a document call render_document with document_id and expected_revision; pass an idempotency_key when you might retry a new render. Failed calls return error.code, retryable and details[] (JSON pointer path and hint per finding): fix those, do not retry unchanged. No stored template fits a one-off? Pass template (an inline spec, {} at least) instead of template_id; save_inline_template keeps it. Download links are public capabilities: share them only with intended readers. get_guide topics: workflow, recipes, markdown, components, templates, typst-layouts, brands, troubleshooting; read templates, typst-layouts and brands before authoring your own.`
