# Typst layouts

The contract a custom `layout.typst` must follow: inputs, imports, fonts, checks and an example.

Most templates use a base layout (`layout.base`) and need no Typst. Write a
custom layout only when the PDF needs a different structure (another cover,
a sidebar, a different header). The DOCX output is not affected by
`layout.typst`; it keeps the brand styling.

## How Folio compiles the PDF

Folio converts the expanded document to a Typst fragment `body.typ` and
compiles this fixed entry point:

```typst
#import "layout.typ": folio-layout
#show: folio-layout.with(
  meta: json("meta.json"), brand: json("brand.json"),
  labels: json("labels.json"), options: json("options.json"),
)
#include "body.typ"
```

`layout.typ` is your `layout.typst` source, or `#import "/base/<base>.typ": folio-layout`
when it is `null`. Your source must define:

```typst
#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = { ... }
```

## Inputs

`meta` (dictionary): the effective metadata: `title`, `date`
(`YYYY-MM-DD`), `language`, and when present `author`, `client`, `project`,
`reference`, `version_label`, `confidentiality`, plus the template's own
metadata fields. Use `meta.at("client", default: none)` for optional keys.

`brand` (dictionary):

| key | value |
|---|---|
| `name` | brand name |
| `colors.primary`, `colors.secondary`, `colors.accent`, `colors.text`, `colors.muted`, `colors.background` | `#RRGGBB` strings: use `rgb(brand.colors.primary)` |
| `fonts.heading`, `fonts.body`, `fonts.mono` | bundled font family names |
| `logo.path`, `logo.width_mm` | present only when the brand has a logo; `logo.path` is relative to the job root, so write `image("/" + brand.logo.path, width: brand.logo.width_mm * 1mm)` |
| `header.text`, `header.show_logo` | header text and whether to show the logo |
| `footer.text`, `footer.legal`, `footer.page_numbers` | footer lines and page numbering |

`labels` (dictionary): the resolved label table for `meta.language`, e.g.
`labels.at("page")`, `labels.at("toc.title")`, `labels.at("cover.prepared_for")`
(keys in `get_guide('templates')`).

`options` (dictionary): `cover` (bool), `toc` (bool), `page_size`
(`"a4"` or `"letter"`), `heading_numbering` (bool), with defaults applied.

`body`: the document content. Headings, tables (components become tables),
figures and footnotes arrive as ordinary Typst content, so `show` rules for
`heading`, `table`, `figure` and `link` style them.

## Imports

Every `#import` and `#include` must use a string literal that is:

- a base layout or its helpers: `"/base/proposal.typ"`, `"/base/report.typ"`,
  `"/base/quote.typ"`, `"/base/letter.typ"`, `"/base/common.typ"`;
- a relative path inside the layout (rarely useful: only `layout.typ`,
  `body.typ` and the JSON inputs exist);
- an allowlisted package listed in `get_capabilities.typst_packages` (written
  exactly, e.g. `"@preview/name:1.0.0"`).

Anything else is rejected before compiling (`invalid_typst_import`). There is
no network access: a package that is not vendored fails with `render_failed`
however the import is written. Typst cannot read files outside the job.

`/base/common.typ` exports the building blocks the base layouts use:
`folio-page-setup(meta, brand, labels, options, body)` (page size, margins,
header, footer, fonts, colours, heading and table styles),
`folio-cover(meta, brand, labels)`, `folio-toc(labels)` and
`folio-get(dict, key, default: none)`.

## Fonts

Only the bundled fonts exist: Inter, Source Serif 4, JetBrains Mono and Noto
Sans for text, and Noto Sans Math for equations. Use the brand's fonts
(`brand.fonts.body` and so on) with `"Noto Sans"` as fallback, and keep
`show math.equation: set text(font: "Noto Sans Math")` if you replace the
text font rules, or math fails to render.

## Checks that apply to your layout

- Compilation errors fail the render with `render_failed` and Typst's
  message (paths shown relative to the job).
- Typst warnings become `typst_warning` findings.
- Each page is rasterised and checked: a page with almost no ink outside the
  20 mm header and footer bands is a `blank_page` warning; any ink within
  2 mm of the page edge is an `edge_ink` warning (content clipped or
  overflowing). Full-bleed backgrounds trigger it: keep a 2 mm margin or use
  the brand background colour as page fill.
- More than the page limit (default 200) fails with `too_many_pages`.
- Each external process has a time limit (default 60 s); an endless loop is
  killed and reported as `timeout`.

Because `create_template` refuses samples with non-placeholder warnings, a
layout that produces blank pages or edge ink cannot be saved.

## Example: extend the proposal layout

```typst
#import "/base/proposal.typ": folio-layout as base-layout
#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = base-layout(
  meta: meta, brand: brand, labels: labels, options: options,
  { set par(justify: false); body },
)
```

## Example: a layout from the building blocks

```typst
#import "/base/common.typ": folio-page-setup, folio-cover, folio-get

#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = {
  show: folio-page-setup.with(meta, brand, labels, options)
  if options.cover { folio-cover(meta, brand, labels) }
  let conf = folio-get(meta, "confidentiality")
  if conf != none {
    block(fill: rgb(brand.colors.accent).lighten(70%), inset: 6pt, radius: 3pt)[#strong(conf)]
  }
  show heading.where(level: 1): it => block(above: 18pt, below: 8pt)[
    #text(fill: rgb(brand.colors.secondary))[#it]
  ]
  body
}
```
