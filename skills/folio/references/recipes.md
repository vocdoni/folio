<!-- Generated from internal/mcpserver/guides/recipes.md by `go generate ./internal/skill`. Do not edit. -->

# Recipes

Step-by-step recipes for common jobs: a proposal, a quote, a statement of work, revising a colleague's document, and creating a brand and template.

Each recipe names the tools to call in order. The starter templates
(`proposal`, `quote`, `sow`, `requirements`, `technical-report`) exist on a
fresh instance; an operator may have renamed, archived or replaced them, so
always start with `list_templates`.

## Proposal

1. `list_templates(category="proposal")`, then `get_template("proposal")`.
2. Start from `body_skeleton`. Keep the required sections (Executive
   summary, Scope, Budget); add Context, Approach, Plan, Deliverables,
   Terms and Acceptance when you have content for them.
3. Put every price in `data.budget.items` (quantities and prices as
   strings). Add `data.milestones` with `payment_pct` values that sum to 100
   when the plan uses `::: {.folio-milestones show_payments=true}`, and
   `data.deliverables` whose `id`s the milestones reference.
4. Metadata: `title`, `date`, `language` and `client` (required by this
   template), plus `project`, `reference`, `valid_until` when known.
5. `validate_document`, fix every error, then `render_document` with an
   `idempotency_key`. Share the PDF and DOCX links and quote the total from
   `computed.budget.total`.

The template's `sample` is a complete proposal: copy its shape.

## Quote

A quote is short: a summary, the priced items, payment terms, validity and
acceptance. This is a complete `render_document` call for the starter
`quote` template:

```json
{
  "template_id": "quote",
  "idempotency_key": "quote-acme-hosting-2026-10-18",
  "metadata": {"title": "Hosting and support quote", "date": "2026-10-18", "language": "en",
               "client": "ACME Corp", "reference": "Q-2026-118", "valid_until": "2026-11-17"},
  "markdown": "# Summary\n\nManaged hosting and support for the ACME website for one year.\n\n::: folio-info\n:::\n\n# Items\n\n::: folio-budget\n:::\n\n# Payment terms\n\n::: {.folio-milestones show_payments=true}\n:::\n\n# Validity\n\nThis quote is valid for 30 days.\n\n# Acceptance\n\n::: folio-signatures\n:::\n",
  "data": {
    "budget": {"currency": "EUR", "tax_rate": "21", "items": [
      {"description": "Managed hosting", "quantity": "12", "unit": "month", "unit_price": "180"},
      {"description": "Support hours", "quantity": "20", "unit": "hour", "unit_price": "85", "discount_pct": "10"}
    ]},
    "milestones": [{"name": "Order", "payment_pct": "50"}, {"name": "Go-live", "payment_pct": "50"}],
    "signatures": [{"name": "Jane Doe", "role": "CEO", "organization": "ACME Corp"},
                   {"name": "Alex Smith", "role": "Account manager", "organization": "Example Co"}]
  }
}
```

Folio computes `subtotal` 3690.00, tax 774.90 and `total` 4464.90, and two
payments of 2232.45. For a Spanish or Catalan quote set `language` to `es`
or `ca` and write the headings in that language (`Resumen`, `Conceptos`, …):
the template's section aliases accept them and amounts are formatted as
`4.464,90 €`.

## Statement of work

1. `get_template("sow")`. Required sections: Objectives, Scope of work,
   Deliverables, Timeline and Fees.
2. List each deliverable once in `data.deliverables` (`id`, `name`,
   `acceptance` criteria) and reference the ids from
   `data.milestones[].deliverables`, so the timeline and the deliverables
   table agree.
3. Put out-of-scope items in their own section: it prevents disputes later.
4. Fees: `data.budget`; payment milestones through `payment_pct`.
5. Validate, render, review the pages with `include_previews="all"`, share.

## Revise a colleague's document

1. Find it: `list_documents(query="ACME")` or `list_documents(created_by="alice")`.
2. `get_document(id, include_source=true)`. Keep `latest_revision`; the
   `source` holds the exact `markdown`, `metadata` and `data` of that
   revision.
3. Change only what was asked, then `render_document` with the same
   arguments plus `document_id` and `expected_revision` = `latest_revision`.
   Revisions need no `idempotency_key`: `expected_revision` protects them.
4. On `conflict`, someone else revised it first: read it again and re-apply
   your change. A result with `unchanged: true` means your input was
   identical to the latest revision.
5. When the document is final, `publish_document(id, revision)`.

## Create a brand and a template

1. Read `get_guide('brands')` and `get_guide('templates')`; read
   `get_guide('typst-layouts')` only if a base layout is not enough.
2. `get_capabilities` for fonts, base layouts, components and label keys.
3. `upload_asset` the logo (SVG preferred), then `create_brand` with the
   returned `asset_id`.
4. `get_template` of the closest starter template; change `id`, `name`,
   `brand_id`, sections, schemas, `body_skeleton` and `sample`; then
   `create_template`. The sample must render without errors or non
   placeholder warnings.
5. `preview_template(id, include_previews="all")` and look at every page;
   fix with `update_template(id, expected_version, template)`.
6. Render a first real document with the new template to confirm.
