# Markdown

The Markdown dialect Folio accepts, how images, links and sections work, and the input limits.

Folio reads the document body with pandoc's Markdown reader, with raw HTML,
raw TeX, raw attributes, YAML metadata blocks, citations and implicit heading
references switched off (`markdown-raw_html-raw_tex-raw_attribute-yaml_metadata_block-citations-implicit_header_references`).
Everything you write becomes text, never code that the renderer executes.

## Supported

| Element | Syntax |
|---|---|
| Headings | `# Title` … `###### Title`; give an explicit id with `# Title {#scope}` |
| Emphasis | `*italic*`, `**bold**`, `~~struck~~`, `H~2~O`, `x^2^` |
| Lists | `- item`, `1. item`, nested by indenting 2–4 spaces |
| Task lists | `- [ ] open`, `- [x] done` |
| Tables | pipe tables (recommended) and grid tables; every row needs as many cells as the header |
| Footnotes | `text[^1]` and, on its own line, `[^1]: The note.` |
| Code | `` `inline` `` and fenced blocks with ```` ``` ```` |
| Quotes | `> quoted text` |
| Links | `[text](https://example.com)`, `[text](mailto:a@example.com)`, `[text](#scope)` |
| Images | `![Caption](asset:<asset_id>)` only (see below) |
| Math | `$E = mc^2$` inline, `$$ ... $$` display |
| Components | empty fenced divs such as `::: folio-budget` + `:::` (see `get_guide('components')`) |
| Typography | straight quotes, `--` and `...` become typographic quotes, dashes and ellipses |

Pipe table example:

```markdown
| Phase     | Weeks | Owner  |
|-----------|------:|--------|
| Discovery |     2 | Client |
| Build     |     6 | Us     |
```

## Images

Upload the file with `upload_asset` (PNG, JPEG, WebP or SVG) and use its id:

```markdown
![Architecture overview](asset:0192f0c1-7d2e-7b3a-9c4d-5e6f7a8b9c0d)
```

An image alone in its paragraph becomes a figure with the caption below it.
Any other target (`https://…`, a file name, `data:`) is rejected with
`invalid_image_target`; an unknown or deleted asset gives `missing_asset`.
DOCX output receives a PNG rendition of SVG images automatically.
To fix an image's name or description (alt text) without changing its id, use
`update_asset`.

## Links

Links may point to `http://` or `https://` URLs, `mailto:` addresses or a
heading in the same document (`#id`). Other schemes (`file:`, `javascript:`,
relative paths) are rejected with `invalid_link`. Headings get automatic ids
(`# Project scope` becomes `#project-scope`) or explicit ones
(`# Scope {#scope}`), but the `[Scope]` shorthand is off: write
`[see Scope](#scope)`.

## What is not supported

- Raw HTML and raw TeX are not interpreted: `<b>` or `\newpage` appear as
  literal text. Use Markdown instead.
- ```` ```{=typst} ```` and other raw blocks are shown as code, never run.
- A YAML front-matter block is not read as metadata. Send metadata in the
  `metadata` argument.
- Citations (`[@key]`) are plain text.
- Fenced divs other than `folio-*` components are accepted but have no
  visual effect, and there is no manual page break: the layout decides
  pagination.

## Sections

Templates declare sections. A section is present when a heading's text (any
level) matches the section title or one of its aliases, ignoring case and
repeated spaces. Missing required sections fail validation with
`missing_section` and the template's hint. A top-level (`#`) heading that is
not a template section ends the current section; components that a template
ties to a section must appear under that section's heading.

## Placeholders

Folio flags leftover placeholders in text (not in code or math):

- the words `TODO`, `TBD`, `FIXME`, `XXX` (upper case, whole words);
- `[...]` and `[…]`;
- `{{anything}}`;
- `lorem ipsum` in any case.

They are warnings (`placeholder`), or errors when the template is `strict`.
Template skeletons use `TODO` to mark what you must write.

## Limits

Defaults (the instance's values are in `get_capabilities.limits`):

- Markdown size: 1 MiB (`markdown_too_large`).
- Nesting of lists, block quotes and divs: 32 levels (`nesting_too_deep`); chained markers on one line (`- - - x`) count one level each.
- Table size: 10 000 cells per table (`table_too_large`); rows with a
  different cell count than the header give `malformed_table`.
- Rendered output: 200 pages (`too_many_pages`).

## A complete example

```markdown
# Executive summary

Example Co proposes a redesign of the public website so customers find
products faster.

::: folio-info
:::

# Scope

- Information architecture
- Design system and page templates
- Content migration of **120** product pages[^pages]

[^pages]: Pages beyond 120 are quoted separately.

# Budget

::: folio-budget
:::
```
