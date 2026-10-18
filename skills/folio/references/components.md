<!-- Generated from internal/mcpserver/guides/components.md by `go generate ./internal/skill`. Do not edit. -->

# Components

Structured blocks (budget, milestones, deliverables, signatures, info): their data, rules and computed values.

A component is an empty fenced div with a `folio-` class. Folio replaces it
with generated content built from `data.<component>` (or from metadata, for
`info`). Data values are always inserted as plain text, so they can never
inject formatting.

```markdown
# Budget

::: folio-budget
:::
```

## Placement rules

- The div must be empty: write the opening and closing `:::` on consecutive
  lines (`component_not_empty` otherwise).
- It must be a top-level block, not inside a list or quote
  (`misplaced_component`).
- The template must list it in `components` (`component_not_allowed`);
  unknown `folio-*` classes give `unknown_component`.
- When a template section declares the component (`"component": "budget"`),
  the div must sit under that section's heading (`misplaced_component`). A
  present section without its div gives the warning `component_missing`.
- `data.<name>` is required for every component used except `info`
  (`schema_violation` at `/data/<name>`), and must match the component schema
  (published by `get_capabilities.components`).

## Money rules

Folio does all arithmetic with exact decimals; never compute amounts yourself.

- Amounts, quantities and percentages may be JSON strings or numbers; strings
  (`"1500.50"`) are recommended. They are read from their decimal text, never
  as floating point.
- `quantity` and `unit_price`: at most 4 decimals and 12 integer digits;
  `tax_rate`, `discount_pct` and `payment_pct`: at most 2 decimals. All must
  be ≥ 0 and percentages ≤ 100.
- `currency` is an ISO 4217 code (`EUR`, `USD`, `GBP`, `CHF`, …); amounts are
  rounded half-up to its minor unit (2 decimals for most, 0 for `JPY`).
- Violations are `money_invalid` errors pointing at the field
  (`/data/budget/items/2/unit_price`).

Formatting follows `metadata.language`:

| language | amount | percent |
|---|---|---|
| `en` and languages without built-in formatting | `€1,234.56`, `CHF 1,234.56` | `21%` |
| `es`, `ca` | `1.234,56 €`, `1.234,56 CHF` | `21 %` |

Symbols are used for EUR (€), USD ($), GBP (£) and JPY (¥); other currencies
print their code.

## budget

Data:

| field | required | meaning |
|---|---|---|
| `currency` | yes | ISO 4217 code |
| `items` | yes | 1–500 lines: `description` (required), `quantity` (required), `unit`, `unit_price` (required), `tax_rate`, `discount_pct` |
| `tax_rate` | no | default tax rate (%) for lines without their own |
| `discount_pct` | no | document discount (%) applied to every line |
| `notes` | no | paragraph printed under the table (≤ 2000 characters) |
| `expected_total` | no | the total you expect; validation fails with `expected_total_mismatch` if Folio computes a different one |

Algorithm (each step rounded half-up to the currency's minor unit):

1. line `net = quantity × unit_price × (1 − discount_pct/100)`;
   `subtotal` = sum of line nets;
2. with a document `discount_pct`, each line becomes
   `discounted = net × (1 − discount_pct/100)`; `discounted_subtotal` = sum
   of discounted lines;
3. lines are grouped by tax rate (the line's `tax_rate`, else the budget's,
   else 0); each group's tax = `sum(discounted lines) × rate / 100`;
4. `total = discounted_subtotal + sum of group taxes`.

Rendered as a table (Description, Qty, Unit when any line has one, Unit
price, Discount when any line has one, Amount) followed by Subtotal, the
document discount, one tax row per rate and the Total.

```json
{"budget": {
  "currency": "EUR", "tax_rate": "21",
  "items": [
    {"description": "Discovery workshop", "quantity": "1", "unit": "day", "unit_price": "1200"},
    {"description": "Design system", "quantity": "12", "unit": "day", "unit_price": "650", "discount_pct": "10"},
    {"description": "Hosting (exempt)", "quantity": "12", "unit": "month", "unit_price": "40", "tax_rate": "0"}
  ],
  "notes": "Valid for 60 days."
}}
```

`computed.budget` (returned by `validate_document` and `render_document`):
`currency`, `lines[]` (`description`, `quantity`, `unit`, `unit_price`,
`discount_pct`, `tax_rate`, `net`, `discounted`), `subtotal`,
`discount_pct`, `discount_amount`, `discounted_subtotal`, `tax_groups[]`
(`rate`, `base`, `tax`), `tax_total`, `total`, `notes`. Quote these values;
do not recompute them.

## milestones

Data: `data.milestones`, 1–200 entries:

| field | required | meaning |
|---|---|---|
| `name` | yes | milestone name |
| `date` | no | `YYYY-MM-DD` |
| `duration` | no | free text, e.g. `2 weeks` (shown after the date) |
| `description` | no | |
| `deliverables` | no | list of deliverable ids, e.g. `["D1", "D2"]` |
| `payment_pct` | no | share of the budget total paid at this milestone |

Rendered as a table: Milestone and When, then Description and Deliverables
when any milestone has them.

With `::: {.folio-milestones show_payments=true}` a Payment column shows
`30% · €4,500.00` for each milestone with `payment_pct`. Then:

- the document must also use the `budget` component (`money_invalid`
  otherwise);
- the `payment_pct` values present must add up to exactly 100
  (`payment_pct_sum`); milestones without one get no payment;
- each amount is the budget total × pct, rounded; the last payment absorbs
  the rounding remainder so payments always add up to the total.

`computed.milestones`: `currency` and `items[]` (`pct`, `amount`), aligned
with `data.milestones`.

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
present, with labels in the document language.

## Labels

Table headers and totals use the built-in label tables for `en`, `es` and
`ca` (for example `budget.total`, `milestones.payment`). Templates can
override any label per language in `labels`; `get_capabilities.label_keys`
lists the keys.
