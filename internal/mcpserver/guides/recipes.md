# Recipes

Step-by-step recipes for common jobs: a proposal, a quote, a statement of work, revising a colleague's document, a one-off document without a stored template, and creating a brand and template.

Each recipe names the tools to call in order. The starter templates
(`proposal`, `quote`, `sow`, `requirements`, `technical-report`) exist on a
fresh instance; an operator may have renamed, archived or replaced them, so
always start with `list_templates`. Folio never changes your content or
figures: send every value exactly as it should appear, totals included.

## Proposal

1. `list_templates(category="proposal")`, then `get_template("proposal")`.
2. Start from `body_skeleton`. Keep the required sections (Executive
   summary, Scope, Budget); add Context, Approach, Plan, Deliverables,
   Terms and Acceptance when you have content for them.
3. Put every line in `data.budget.items` and the subtotal, tax and total
   rows in `data.budget.totals`, all as strings written exactly as they
   should appear (`"€18,280.00"`). Work the figures out with the user before
   calling Folio: Folio prints them as sent and never checks the sums. Add
   `data.milestones` (with a `payment` text per milestone when the plan has
   payments) and `data.deliverables` whose `id`s the milestones reference.
4. Metadata: `title`, `date`, `language` and `client` (required by this
   template), plus `project`, `reference`, `valid_until` when known.
5. `validate_document`, fix every error, then `render_document` with an
   `idempotency_key`. Share the PDF and DOCX links.

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
  "markdown": "# Summary\n\nManaged hosting and support for the ACME website for one year.\n\n::: folio-info\n:::\n\n# Items\n\n::: folio-budget\n:::\n\n# Payment terms\n\n::: folio-milestones\n:::\n\n# Validity\n\nThis quote is valid for 30 days.\n\n# Acceptance\n\n::: folio-signatures\n:::\n",
  "data": {
    "budget": {
      "items": [
        {"description": "Managed hosting", "quantity": "12", "unit": "month", "unit_price": "€180.00", "amount": "€2,160.00"},
        {"description": "Support hours", "quantity": "20", "unit": "hour", "unit_price": "€85.00", "discount": "10%", "amount": "€1,530.00"}
      ],
      "totals": [
        {"label": "Subtotal", "amount": "€3,690.00"},
        {"label": "VAT (21%)", "amount": "€774.90"},
        {"label": "Total", "amount": "€4,464.90", "emphasis": true}
      ]
    },
    "milestones": [{"name": "Order", "payment": "50% · €2,232.45"}, {"name": "Go-live", "payment": "50% · €2,232.45"}],
    "signatures": [{"name": "Jane Doe", "role": "CEO", "organization": "ACME Corp"},
                   {"name": "Alex Smith", "role": "Account manager", "organization": "Example Co"}]
  }
}
```

Folio prints these figures exactly as written; the arithmetic (12 × €180.00,
20 × €85.00 less 10%, VAT and the two halves) is yours. For a Spanish or
Catalan quote set `language` to `es` or `ca`, write the headings in that
language (`Resumen`, `Conceptos`, …), which the template's section aliases
accept, and write the figures the local way (`4.464,90 €`).

## Statement of work

1. `get_template("sow")`. Required sections: Objectives, Scope of work,
   Deliverables, Timeline and Fees.
2. List each deliverable once in `data.deliverables` (`id`, `name`,
   `acceptance` criteria) and reference the ids from
   `data.milestones[].deliverables`, so the timeline and the deliverables
   table agree.
3. Put out-of-scope items in their own section: it prevents disputes later.
4. Fees: `data.budget` with your own `totals` rows; payment terms as the
   `payment` text of each milestone (`"25% · €7,139.00"`).
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

## A one-off document (inline template)

For a document no stored template fits, such as a single thank-you letter,
send the template with the call instead of creating one:

1. `get_capabilities`: `default_brand` is the brand used when you pass no
   `brand_id`; when it is empty, pick one with `list_brands`.
2. `validate_document` with `template` instead of `template_id`, then
   `render_document` with the same arguments and an `idempotency_key`:

```json
{
  "template": {"layout": {"base": "letter"}, "sections": [{"title": "Greeting", "required": true}]},
  "brand_id": "example",
  "idempotency_key": "thank-you-globex-2026-10-19",
  "metadata": {"title": "Thank you", "date": "2026-10-19", "language": "en", "client": "Globex"},
  "markdown": "# Greeting\n\nThank you for choosing us for the Globex website."
}
```

3. Revise it like any document (no `template` needed: it keeps its inline
   template). When the user wants the same layout again,
   `save_inline_template(document_id, id, name)` makes it a regular
   template; then use `template_id` = that id.

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
