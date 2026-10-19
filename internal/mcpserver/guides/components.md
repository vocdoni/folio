# Components

Structured blocks (budget, milestones, deliverables, signatures, info): their data, placement rules and how they render.

A component is an empty fenced div with a `folio-` class. Folio replaces it
with a table built from `data.<component>` (or from metadata, for `info`).
Data values are always inserted as plain text, so they can never inject
formatting.

```markdown
# Budget

::: folio-budget
:::
```

## Placement rules

- The div must be empty: write the opening and closing `:::` on consecutive
  lines (`component_not_empty` otherwise).
- It takes no attributes: `::: {.folio-milestones key=value}` fails with
  `component_attribute`. Write `::: folio-milestones`; everything a component
  shows comes from `data`.
- It must be a top-level block, not inside a list or quote
  (`misplaced_component`).
- The template must list it in `components` (`component_not_allowed`);
  unknown `folio-*` classes give `unknown_component`.
- When a template section declares the component (`"component": "budget"`),
  the div must sit under that section's heading (`misplaced_component`). A
  present section without its div gives the warning `component_missing`.
  At most one template section declares a component: a second declaration is
  a template error, so a component cannot repeat across sections.
- `data.<name>` is required for every component used except `info`
  (`schema_violation` at `/data/<name>`), and must match the component schema
  (published by `get_capabilities.components`).

## Figures are yours

Folio never changes your content or figures:
send every value exactly as it should appear.
Folio does not calculate, round, check or reformat amounts, quantities,
percentages, totals or payments; it lays them out.

- Every displayed value is a JSON string: `"€1,234.50"`, `"1.234,50 €"`,
  `"12 days"`, `"TBD"`. A JSON number fails with `schema_violation` at its
  path and the hint `send figures as strings exactly as they should appear`.
- Write figures in the document's language and currency style; Folio prints
  them character for character, including no-break spaces.
- You add the total rows (subtotal, discount, tax, total, …) yourself in
  `data.budget.totals`, with your own labels and amounts. Do the arithmetic
  before you call Folio and check it: Folio will not notice a wrong sum.

## budget

Data: `data.budget`:

| field | required | meaning |
|---|---|---|
| `items` | yes | 1–500 lines, see below |
| `totals` | no | up to 20 rows printed under the lines, in order: `label` (required), `amount` (required), `emphasis` (bool: bold row) |
| `notes` | no | paragraph printed under the table (≤ 2000 characters) |

Each line of `items`:

| field | required | column |
|---|---|---|
| `description` | yes | Description |
| `quantity` | no | Qty |
| `unit` | no | Unit (≤ 32 characters) |
| `unit_price` | no | Unit price |
| `discount` | no | Discount |
| `amount` | yes | Amount |

Description and Amount are always shown; Qty, Unit, Unit price and Discount
only when at least one line has a value for them. Column headers come from
the label tables (see Labels); total-row labels come from your data.

```json
{"budget": {
  "items": [
    {"description": "Discovery workshop", "quantity": "1", "unit": "day", "unit_price": "€1,200.00", "amount": "€1,200.00"},
    {"description": "Design system", "quantity": "12", "unit": "day", "unit_price": "€650.00", "discount": "10%", "amount": "€7,020.00"},
    {"description": "Hosting (VAT exempt)", "quantity": "12", "unit": "month", "unit_price": "€40.00", "amount": "€480.00"}
  ],
  "totals": [
    {"label": "Subtotal", "amount": "€8,700.00"},
    {"label": "VAT (21% on €8,220.00)", "amount": "€1,726.20"},
    {"label": "Total", "amount": "€10,426.20", "emphasis": true}
  ],
  "notes": "Valid for 60 days."
}}
```

## milestones

Data: `data.milestones`, 1–200 entries:

| field | required | meaning |
|---|---|---|
| `name` | yes | milestone name |
| `date` | no | `YYYY-MM-DD` |
| `duration` | no | free text, e.g. `2 weeks` (shown after the date) |
| `description` | no | |
| `deliverables` | no | list of deliverable ids, e.g. `["D1", "D2"]` |
| `payment` | no | text shown in the Payment column, e.g. `30% · €4,500.00` or `On signature` |

Rendered as a table: Milestone and When, then Description, Deliverables and
Payment when any milestone has them. `payment` is printed exactly as sent;
make the payments add up yourself.

When the document also uses `::: folio-deliverables`, every id listed in a
milestone's `deliverables` must exist in `data.deliverables`
(`unknown_deliverable` at `/data/milestones/<i>/deliverables/<j>`).

## deliverables

Markdown: `::: folio-deliverables` + `:::`. Data: `data.deliverables`,
1–200 entries: `id` (required, ≤ 64 characters), `name` (required),
`description`, `due` (free text), `acceptance`. Rendered as a table: ID and
Deliverable, then Description, Due and Acceptance criteria when any entry has
them. Milestones refer to deliverables by `id`.

## signatures

Markdown: `::: folio-signatures` + `:::`. Data: `data.signatures`, 1–12
entries: `name` (required), `role`, `organization`. Rendered as signature blocks, up to three per row: a
signature line, the name in bold, role and organization, and a date line.

## info

Markdown: `::: folio-info` + `:::`. No data. Renders a two-column table of the metadata fields `client`,
`project`, `reference`, `date`, `author` and `version_label` that are
present, with labels in the document language. It is not a general-purpose
table: write any other table as a Markdown pipe table in the section text
(section `component` empty). Use `info` once per document, or not at all.

## Labels

Table headers use the built-in label tables for `en`, `es` and `ca` (for
example `budget.amount`, `milestones.payment`). Templates can override any
label per language in `labels`; `get_capabilities.label_keys` lists the keys.
Total-row labels are not label keys: they are the `label` of each
`data.budget.totals` row.
