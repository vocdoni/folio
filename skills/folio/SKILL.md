---
name: folio
description: Create branded, consistently formatted PDF and editable DOCX business documents (proposals, quotes, budgets, statements of work, requirements, technical reports, letters) with a Folio server over MCP. Use when the user asks to write, format, brand, render, revise, publish or share such a document, to lay out a budget or quote with line items, totals and payment terms, or to create and manage Folio templates, brands and assets. Folio never changes content or figures, so send every value exactly as it should appear.
license: AGPL-3.0-only
compatibility: Needs a Folio server connected as an MCP server (Streamable HTTP with a bearer token), for example through the Folio Claude Code plugin or `claude mcp add --transport http`.
---

# Folio

Folio turns Markdown, metadata and typed data into a branded PDF and an
editable DOCX, using templates and brands shared by everyone on the Folio
instance. You write the content, figures included; Folio checks its
structure, renders both files exactly as written and returns download links.

## Before you start

The Folio tools (`list_templates`, `get_template`, `validate_document`,
`render_document`, …) come from the Folio MCP server. If they are not
available, ask the user to connect Folio (Claude Code plugin
`/plugin install folio@folio`, or
`claude mcp add --transport http folio https://<folio-host>/mcp --header "Authorization: Bearer <token>"`)
instead of formatting the document yourself.

## Core workflow

1. `list_templates` and pick the template that fits (ask when several fit).
2. `get_template(id)`: read `sections`, `metadata_schema`, `data_schema`,
   `components`, `body_skeleton` and `sample`.
3. `upload_asset` each image once and reference it as
   `![Caption](asset:<asset_id>)`.
4. Write the Markdown from `body_skeleton`; put every figure in `data` as a
   string written exactly as it should appear, including the budget's
   `totals` rows.
5. `validate_document` with the same arguments; fix every `error` finding
   (each has a JSON pointer `path` and a `hint`) and validate again.
6. `render_document` with an `idempotency_key`; look at the warnings and the
   preview image.
7. Share the PDF and DOCX links.

## Rules

- Folio never changes your content or figures: send every value exactly as
  it should appear. It does not calculate or check totals, taxes, discounts
  or payments, so work them out and double-check them before rendering.
- Validate before rendering; never retry an unchanged call that failed with
  `validation_failed` or `timeout`. Retry `busy` after a few seconds with the
  same arguments and `idempotency_key`.
- Revise with `render_document(document_id, expected_revision)` after
  `get_document(id, include_source=true)`; on `conflict`, re-read and
  re-apply.
- Download links work without a token: share them only with intended
  readers. Everything on an instance is shared by all its users.
- AI review findings (`review_document`) are advisory only.

## References

Each reference is the same text the Folio server returns from
`get_guide(topic)`; read the one you need, not all of them.

- [references/workflow.md](references/workflow.md): Workflow. Which Folio tools to call, in which order, for every common task.
- [references/recipes.md](references/recipes.md): Recipes. Step-by-step recipes for common jobs: a proposal, a quote, a statement of work, revising a colleague's document, and creating a brand and template.
- [references/markdown.md](references/markdown.md): Markdown. The Markdown dialect Folio accepts, how images, links and sections work, and the input limits.
- [references/components.md](references/components.md): Components. Structured blocks (budget, milestones, deliverables, signatures, info): their data, placement rules and how they render.
- [references/templates.md](references/templates.md): Templates. Every field of a template spec, how schemas, sections, samples and labels work, and how versions behave.
- [references/typst-layouts.md](references/typst-layouts.md): Typst layouts. The contract a custom `layout.typst` must follow: inputs, imports, fonts, checks and an example.
- [references/brands.md](references/brands.md): Brands. Every field of a brand (colours, fonts, logo, header, footer, DOCX fallbacks) and how brand versions are used.
- [references/troubleshooting.md](references/troubleshooting.md): Troubleshooting. Every error code and finding code Folio returns, what it means and how to fix it.
