# Workflow

Which Folio tools to call, in which order, for every common task.

Folio turns Markdown, metadata and typed data into a branded PDF and an
editable DOCX. Templates and brands are shared by everyone on the instance.
Folio never changes your content or figures:
send every value exactly as it should appear.
Figures, total rows and payment terms are printed as you send them; Folio
does not calculate, round or check them, so get them right before rendering.

## Write a new document

1. `list_templates` (filter with `query` or `category`) and pick the template
   that fits. Ask the user when several fit.
2. `get_template(id)`. Read:
   - `sections`: the headings the document needs (`required: true` ones must
     be present; `aliases` are accepted alternatives, e.g. other languages;
     `hint` says what goes there);
   - `metadata_schema`: extra metadata fields (always add `title`, `date` as
     `YYYY-MM-DD` and `language`, one of `languages`);
   - `data_schema` and `components`: which structured data the document takes
     (see `get_guide('components')`);
   - `body_skeleton`: start your Markdown from it;
   - `sample`: a complete, valid example of markdown, metadata and data.
3. If the document has images, upload each once with `upload_asset` and
   reference it as `![Caption](asset:<asset_id>)`. Reuse ids from
   `list_assets` instead of uploading twice.
4. Write the Markdown (see `get_guide('markdown')`), the metadata and the
   data. Put every displayed figure in `data` as a string written the way it
   should appear (for example `"€1,500.00"`, `"1.500,00 €"`), including the
   budget's total rows in `data.budget.totals`; JSON numbers are rejected.
5. `validate_document` with the same arguments you will render. Fix every
   finding with `severity: error` (each has a JSON pointer `path` and a
   `hint`), then validate again. Warnings such as `placeholder` do not block
   rendering unless the template is `strict`, but replace placeholders
   before sharing.
6. `render_document`. Always pass an `idempotency_key` (for example a short
   slug plus date) so a retry after a timeout or a dropped connection returns
   the same document instead of creating a duplicate. The result has
   `document_id`, `revision`, download links in `files`, preview links and
   `warnings`, plus the first page as an image (`include_previews`: `none`,
   `first`, `all`).
7. Look at the warnings and the preview, then share the PDF and DOCX links.

## Revise a document (yours or a colleague's)

1. `get_document(id, include_source=true)`. `source` holds the exact input of
   the revision: `markdown`, `metadata`, `data`, `formats`, and the
   `template` and `brand` versions it used. `latest_revision` is the number
   you must pass back.
2. Edit the source and call `render_document` with `document_id` and
   `expected_revision` set to `latest_revision`. `template_id` defaults to
   the document's template and the latest template and brand versions are
   used; pass `template_version` to stay on an older template version.
3. If someone else revised the document in the meantime the call fails with
   `conflict` naming the current revision: re-read, re-apply your change,
   retry. Re-sending identical input returns the existing revision with
   `unchanged: true` and creates nothing.

Every revision is kept; `get_document(id, revision=N)` shows any of them.

## Publish, trash, restore and purge

- `publish_document(id, revision)` marks a revision as final. The document
  status becomes `published`; later revisions stay drafts until published.
- `delete_document(id)` moves a document to the trash: its download links stop
  working immediately. `restore_document(id)` brings it (and its links) back.
  Trashed documents are purged automatically after the trash retention
  (`get_capabilities.limits.trash_retention_hours`).
- `delete_document(id, purge=true)` deletes every revision and file at once;
  it cannot be undone.
- Deleting or purging a document with a published revision requires
  `confirm_published: true`. Confirm with the user first.

## Look at pages

- `render_document` and `get_document` return page images: `include_previews`
  `first` (default), `all` (up to 6 pages) or `none`; `get_document(pages=[3,4])`
  returns specific pages. Images are at most 800 px wide and at most 6 per
  call.
- The first pages are stored with the revision (`previews` links); later
  pages are rendered on demand from the stored PDF.
- `list_documents` finds documents by `query` (title or client),
  `template_id`, `status`, `client` or `created_by` (the token name of the
  author), newest first.

## Optional AI review

`review_document(id, revision?, pages?)` asks a vision model to look at the
rendered pages (blank pages, overflow, overlapping elements, table and
typography problems). Its findings are advisory: they are stored on the
revision and shown by `get_document`, and never change content, status or
validation. Re-running replaces them. When the operator has not configured a
reviewer, the call returns a non-retryable `unavailable`; if the reviewer
fails, it returns a retryable `unavailable`;
`get_capabilities.reviewer_enabled` tells you in advance. Check pages
yourself with `get_document(include_previews='all')` instead.

When a reviewer is configured, `create_template` and `update_template` also
return a `review` of the sample render (`status` `ok` or `unavailable`, plus
advisory `findings`). It is not stored and never blocks the template: read
it, fix what matters with `update_template`, and ignore what does not.

## Templates and brands

Read `get_guide('templates')`, `get_guide('brands')` and, for custom layouts,
`get_guide('typst-layouts')` first. Then:

1. `get_capabilities` for fonts, base layouts, components, label keys and
   limits.
2. A brand: `upload_asset` (the logo, SVG preferred), then `create_brand`.
3. A template: start from `get_template` of a similar one (its output is
   valid `create_template` input), change the id, sections, schemas,
   skeleton and sample, then `create_template`. Folio renders the sample and
   refuses the template if that produces any error or any warning other
   than placeholders.
4. `preview_template(id, include_previews='all')` and look at every page.
5. Change it with `update_template` / `update_brand`, passing
   `expected_version` = the latest version you read (`conflict` otherwise).
   The newest version is used for new renders immediately; existing
   documents keep the versions they were rendered with.
6. `archive_template` / `archive_brand` hide an item and block new renders
   from it; `archived: false` restores it.

## Retries, concurrency and limits

- `busy` (`retryable: true`): the render queue is full; retry in a few
  seconds with the same arguments (and the same `idempotency_key`).
- `timeout`: a tool exceeded the render time limit; simplify the document
  (fewer nested blocks, smaller tables). Retrying unchanged will time out
  again.
- `conflict`: someone changed the item first; re-read and retry with the
  current version or revision.
- `idempotency_key` is scoped to your token and remembered for 24 hours; it
  applies to new documents only (revisions are protected by
  `expected_revision`).
- Long renders send progress notifications (at least every 20 seconds) so
  the connection stays open.
- `list_*` tools return at most `limit` items (default 50, max 200) and a
  `next_cursor` when there are more; pass it back as `cursor`.

## Links and privacy

Download links (`/f/<id>/<name>`) work without a token: anyone holding a link
can download that file. Share them only with intended readers. Trashing or
purging a document revokes its links; template preview links expire after
the preview retention. Everyone with a token sees and can change every brand,
template and document on the instance; the audit log records who did what.

## Resources and prompts

- `folio://guide/{topic}` serves these guides; `folio://templates/{id}` and
  `folio://documents/{id}` serve the same JSON as `get_template` and
  `get_document`.
- The prompts `new_document(template_id?)` and `new_template(category?)` walk
  through the steps above.

## Tool list

`get_guide`, `get_capabilities`, `list_brands`, `get_brand`, `create_brand`,
`update_brand`, `archive_brand`, `list_templates`, `get_template`,
`create_template`, `update_template`, `archive_template`, `preview_template`,
`upload_asset`, `list_assets`, `delete_asset`, `validate_document`,
`render_document`, `list_documents`, `get_document`, `review_document`,
`publish_document`, `delete_document`, `restore_document`.
