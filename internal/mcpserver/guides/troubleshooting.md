# Troubleshooting

Every error code and finding code Folio returns, what it means and how to fix it.

## Reading an error

A failed tool call has `isError: true` and
`structuredContent.error = {code, message, retryable, details[]}`:

- `code`: one of the error codes below;
- `message`: what went wrong and what to do;
- `retryable`: `true` only when the same call can succeed later (`busy`,
  `unavailable`); otherwise change something before calling again;
- `details`: findings `{code, severity, message, path?, page?, hint?}`.
  `path` is a JSON pointer into your arguments (`/metadata/date`,
  `/data/budget/items/1/quantity`, `/markdown`, `/layout/typst`, or a spec
  field such as `/colors/primary`); `page` is a rendered page number; `hint`
  says how to fix it.

`validate_document` reports the same findings without failing
(`valid: false`). Fix every `error`; `warning`s are informative unless the
template is strict.

## Error codes

| code | meaning | what to do |
|---|---|---|
| `invalid_input` | arguments are malformed: unknown or misspelt fields, wrong types, an invalid cursor, both or neither of `content_base64`/`url`, a missing `expected_revision`, `confirm_published` not set, a page number beyond the PDF's pages, `template` together with `template_id` or `template_version`, an inline template without a brand (`pass brand_id`), a template id reserved for inline templates (`inline-…`) | read the message and the tool's input schema; retrying unchanged fails |
| `validation_failed` | the document, template or brand breaks a rule; `details` lists every finding (paths under `/template` point into an inline `template`) | fix each finding (path, hint), then retry |
| `not_found` | the id (or version, revision) does not exist | check it with the matching `list_*` or `get_*` tool |
| `conflict` | `expected_version` / `expected_revision` is stale, the id already exists, or the document is in the trash | re-read the current state, re-apply your change, retry with the version or revision named in the message; restore trashed documents first |
| `archived` | the template or brand is archived | `archive_template(id, false)` / `archive_brand(id, false)`, or choose another |
| `too_large` | an upload exceeds the size limit, an SVG rasterises beyond the pixel or byte limit (50 million pixels), or an inline template or a `layout.typst` exceeds `get_capabilities.limits` (`max_inline_template_bytes`, `max_layout_typst_bytes`) | upload a smaller file (`get_capabilities.limits.max_asset_bytes`); for an SVG, reduce its `width`/`height`; store a large template with `create_template` and keep custom layouts short |
| `busy` | the render queue is full | retry in a few seconds with the same arguments and `idempotency_key` |
| `timeout` | pandoc or typst exceeded the render time limit | simplify: fewer nested blocks, smaller tables, a simpler custom layout |
| `render_failed` | pandoc or typst failed, or an output check failed (invalid PDF/DOCX, too many pages, missing brand logo) | read the message: for custom layouts it quotes Typst's error; fix the layout or the content |
| `unavailable` | an optional or external service is not available: the AI reviewer is not configured or failed, an asset URL could not be downloaded | if the message says retrying will fail (reviewer not configured), do not retry; otherwise retry later, or work without it (upload `content_base64` instead of `url`) |
| `internal` | a server-side problem; details are in the server log | retry once; if it persists, report it to the operator |

## Finding codes

### Metadata and data

| code | meaning | fix |
|---|---|---|
| `schema_violation` | a value does not match the metadata, data, component or spec schema; `path` points at it (also used for invalid brand and template fields). A JSON number where a component expects text carries the hint `send figures as strings exactly as they should appear` | correct the value; `get_template` shows the schemas, `get_capabilities` the component schemas; send figures as strings such as `"€1,250.00"` |
| `invalid_format` | a value has the right type but the wrong format | follow the format in the message |
| `unknown_deliverable` | a milestone lists a deliverable id that is not in `data.deliverables` (checked when the document uses `::: folio-deliverables`) | use an id from `data.deliverables`, or add the deliverable |

### Markdown and structure

| code | meaning | fix |
|---|---|---|
| `markdown_too_large` | the Markdown exceeds the size limit | split the document or move large tables into data |
| `nesting_too_deep` | lists, quotes or divs nest deeper than the limit (a line of chained markers such as `- - - x` counts one level per marker) | flatten the structure |
| `table_too_large` | a table has more cells than the limit | split or summarise it |
| `malformed_table` | a table row has a different number of cells than the header | give every row the same number of cells |
| `missing_section` | a required section heading is missing | add the heading (title or alias); the hint says what goes there |
| `unknown_component` | a `folio-*` div names no known component | use `budget`, `milestones`, `deliverables`, `signatures` or `info` |
| `component_not_allowed` | the template does not enable that component | remove it or use a template that lists it |
| `component_not_empty` | a component div has content | leave it empty; the content comes from `data.<name>` |
| `component_attribute` | a component div has a `key=value` attribute, e.g. `::: {.folio-milestones key=value}` | remove the attribute: write `::: folio-<name>`; milestone payments are `payment` strings in `data.milestones` |
| `misplaced_component` | a component is nested in another block, or outside the section that declares it | make it a top-level block under the right heading |
| `component_missing` (warning) | a section that declares a component has no component div | add the `::: folio-<name>` block under that heading |
| `invalid_image_target` | an image does not point at `asset:<asset_id>` | upload it with `upload_asset` and use `![caption](asset:<id>)` |
| `missing_asset` | the asset does not exist or was deleted (also a brand logo) | check with `list_assets` or upload it again |
| `invalid_link` | a link uses a scheme other than http, https, mailto or `#anchor` | change or remove the link |
| `raw_content` | raw HTML, TeX or Typst, or a metadata block, reached the document | write plain Markdown; send metadata in `metadata` |
| `placeholder` (warning, error if strict) | `TODO`, `TBD`, `FIXME`, `XXX`, `[...]`, `{{…}}` or lorem ipsum in the text | replace it with real content |

### Rendering and output checks

| code | meaning | fix |
|---|---|---|
| `pandoc_warning` (warning) | pandoc reported a problem while reading the Markdown | read the message; usually malformed Markdown |
| `typst_warning` (warning) | Typst reported a warning while typesetting | read the message; for custom layouts fix the layout |
| `invalid_typst_import` | a custom layout imports something other than `/base/<name>.typ`, a relative path or an allowlisted package | see `get_guide('typst-layouts')` |
| `pdf_invalid` | the PDF failed validation | report it; usually a layout problem |
| `too_many_pages` | the PDF exceeds the page limit | shorten the document |
| `blank_page` (warning, `page` set) | a page has almost no content | remove forced breaks or empty sections; check the custom layout |
| `edge_ink` (warning, `page` set) | something touches the outer 2 mm of the page: clipped or overflowing content | shorten long words or URLs, narrow wide tables, keep layout elements inside the margins |
| `docx_invalid` | the DOCX could not be opened | report it |

Template creation and updates fail on any finding of the last two groups
except `placeholder`, so fix layouts until the sample renders cleanly.

## Common situations

- **A total looks wrong.** Folio prints figures exactly as you send them and
  never checks or recomputes them. Fix the amount in `data.budget.totals` (or
  in the line) and render a new revision.
- **An old document or template version fails with `validation_failed`.**
  Its `data` uses the retired budget shape (a currency code, tax rates and
  payment percentages for Folio to compute). Rewrite `data.budget` as `items`
  with `amount` strings plus `totals` rows, and milestone payments as
  `payment` strings, then render again with the latest template version.
- **`conflict` on render_document.** Someone added a revision. Call
  `get_document(id, include_source=true)`, merge your change into the new
  source, and render with the new `expected_revision`.
- **A download link returns 404.** The document was trashed or purged, the
  link was a template preview that expired, or the link is mistyped. Use
  `get_document` for current links (restore trashed documents first).
- **The first render after a retry created a duplicate.** Always pass an
  `idempotency_key` for new documents.
- **`pass brand_id (available brands: …)`.** An inline template names no
  brand and the instance has no default (several live brands and no
  `FOLIO_DEFAULT_BRAND`): pass `brand_id`.
- **`not_found` from `update_template` on an `inline-…` id.** Inline
  templates are immutable: render with a changed `template` (it gets a new
  id), or `save_inline_template` and update the saved template.
- **Text in the wrong language in tables.** Set `metadata.language`; table
  labels follow it (override them with template `labels`).
