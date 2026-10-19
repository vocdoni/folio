# Templates

Every field of a template spec, how schemas, sections, samples and labels work, and how versions behave.

A template is a versioned, shared recipe for one kind of document. Start from
`get_template` of a similar template: its `template` object is valid
`create_template` input, so you can copy it, change the `id` and edit.

## Fields

| field | meaning |
|---|---|
| `id` | slug `^[a-z0-9][a-z0-9-]{1,62}$`, unique on the instance |
| `name`, `description` | shown by `list_templates`; say when to use the template |
| `category` | free text used for filtering, e.g. `proposal`, `quote`, `report`, `sow` |
| `brand_id` | default brand (an existing, unarchived brand); `render_document(brand_id=…)` can override it |
| `languages` | BCP-47 primary tags the template supports, e.g. `["en", "es", "ca"]`; `metadata.language` must be one of them |
| `layout` | `{"base": "proposal", "typst": null, "options": {...}}` (see below) |
| `metadata_schema` | JSON Schema (2020-12) for extra metadata fields |
| `data_schema` | JSON Schema for `data` |
| `sections` | headings the document needs |
| `components` | components the document may use: `budget`, `milestones`, `deliverables`, `signatures`, `info` |
| `strict` | `true` turns placeholder warnings into errors |
| `body_skeleton` | Markdown agents start from: every section heading, component divs, `TODO` where text goes |
| `sample` | `{"markdown", "metadata", "data"}`: a complete realistic document |
| `labels` | per-language overrides of table labels, e.g. `{"es": {"budget.amount": "Importe"}}` |
| `spec_version` | set by Folio (currently 1); keep it when copying |

## Layout

- `base`: one of `report`, `proposal`, `quote`, `letter`. All share the brand
  styling (header with logo, footer with legal text and page numbers,
  heading colours, table style); they differ only in their names today, so
  pick the one that matches the document kind.
- `options` (validated against `get_capabilities.base_layouts[].options_schema`;
  unknown keys are rejected):

| option | default | PDF | DOCX |
|---|---|---|---|
| `cover` | `true` | cover page: logo, title, version label, client, project, reference, date, author | the same fields as styled paragraphs, then a page break |
| `toc` | `false` | table of contents after the cover | Word table of contents on its own page after the cover, before the body (Word may ask to update fields) |
| `page_size` | `"a4"` | `a4` or `letter` | same page size, 20 mm margins |
| `heading_numbering` | `false` | headings numbered 1, 1.1, … | numbered headings |

- `typst`: `null`, or the source of a custom Typst layout that replaces the
  base layout for the PDF (the DOCX keeps the brand styling). Read
  `get_guide('typst-layouts')` before writing one.

## Metadata

Core fields are always part of the effective metadata schema, whatever the
template says:

| field | rule |
|---|---|
| `title` | required, 1–300 characters |
| `date` | required, `YYYY-MM-DD`, a real calendar date (also fixes the PDF timestamp, so renders are reproducible) |
| `language` | required, one of the template's `languages` |
| `author`, `client`, `project` | optional, ≤ 300 characters |
| `reference` | optional, ≤ 100 characters |
| `version_label` | optional, ≤ 50 characters, e.g. `v1.2` |
| `confidentiality` | optional, ≤ 100 characters |

`metadata_schema` adds fields or tightens rules (it is combined with the core
schema with `allOf`):

```json
{"type": "object", "required": ["client"],
 "properties": {"client": {"type": "string", "minLength": 1},
                "valid_until": {"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}}}
```

## Data

`data_schema` describes `data`. Reference the built-in component schemas
with `folio:component/<name>`; no other `$ref` targets (files, URLs) are
resolved.

```json
{"type": "object", "required": ["budget"],
 "properties": {"budget": {"$ref": "folio:component/budget"},
                "milestones": {"$ref": "folio:component/milestones"}}}
```

Each component used in the Markdown is validated against its own schema even
without a `data_schema`.

## Sections

```json
[{"title": "Executive summary", "required": true, "aliases": ["Summary", "Resumen ejecutivo"],
  "hint": "Two or three paragraphs: the problem, the proposal, the outcome."},
 {"title": "Budget", "required": true, "aliases": ["Presupuesto"], "component": "budget"}]
```

- A section is present when a heading of any level matches its `title` or
  one of its `aliases` (case-insensitive). Give aliases for every language in
  `languages`.
- `required` sections missing from a document fail validation
  (`missing_section`) and the error shows `hint`.
- `component` ties a component to the section: its div must appear under that
  heading, and the component must also be listed in `components`. At most
  one section declares a given component; other tables are Markdown pipe
  tables in sections with an empty `component`.

## The sample

`create_template` and `update_template` render the sample with the
template's brand and fail with `validation_failed` when the render produces
any error, or any warning other than `placeholder`. So the sample must be a
complete, valid document that exercises every component, renders without
blank pages or content touching the page edges, and fits the page limit.
`preview_template` re-renders it on demand; preview links expire.

## Labels

Built-in label tables exist for `en`, `es` and `ca`. `labels` overrides
individual keys per language; a language outside the built-in set uses its
table from `labels` only if it defines every key (otherwise English labels
are used). Folio never formats figures: they are printed as sent. Keys:

`budget.description`, `budget.quantity`, `budget.unit`, `budget.unit_price`,
`budget.discount`, `budget.amount`, `milestones.name`, `milestones.date`,
`milestones.description`, `milestones.deliverables`, `milestones.payment`,
`deliverables.id`, `deliverables.name`, `deliverables.description`,
`deliverables.due`, `deliverables.acceptance`, `signatures.signature`,
`signatures.date`, `info.client`, `info.project`, `info.reference`,
`info.date`, `info.author`, `info.version_label`, `cover.prepared_for`,
`toc.title`, `page`.

Budget total rows have no label keys: their labels are the `label` of each
`data.budget.totals` row, written in the document's language. The retired
keys `budget.subtotal`, `budget.tax` and `budget.total` are still accepted in
`labels` and ignored.

## Versions and archiving

- `create_template` creates version 1. `update_template(id, expected_version, template)`
  creates `latest + 1` only when `expected_version` equals the latest
  version; otherwise it fails with `conflict` and names the latest version.
- The newest version is active immediately (there is no deploy step).
  Documents keep the template version they were rendered with; revisions use
  the latest version unless `template_version` is passed.
- `get_template` lists every version with its `note`, author and date; pass
  `version` to read an old one. Always write a `note` saying what changed.
- `archive_template(id, true)` hides the template and makes new renders fail
  with `archived`; `archive_template(id, false)` restores it. Versions are
  never deleted.

## Checklist

1. `id`, `name`, `description`, `category`, `brand_id`, `languages`.
2. Sections with aliases per language, hints for required ones.
3. `metadata_schema` for the extra fields the documents need.
4. `components` and a `data_schema` that references their schemas.
5. A `body_skeleton` with every section heading and component div.
6. A complete sample in one of the languages.
7. `create_template`, then `preview_template(include_previews='all')`.
