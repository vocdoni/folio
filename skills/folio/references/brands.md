<!-- Generated from internal/mcpserver/guides/brands.md by `go generate ./internal/skill`. Do not edit. -->

# Brands

Every field of a brand (colours, fonts, logo, header, footer, DOCX fallbacks) and how brand versions are used.

A brand is the visual identity applied to every document rendered with it:
colours, fonts, logo, header and footer, in both the PDF and the DOCX. Brands
are versioned and shared by everyone on the instance.

## Fields

```json
{
  "id": "example",
  "name": "Example Co",
  "colors": {"primary": "#1F4FD8", "secondary": "#0B1F44", "accent": "#F2A900",
             "text": "#1A1A1A", "muted": "#6B7280", "background": "#FFFFFF"},
  "fonts": {"heading": "Inter", "body": "Source Serif 4", "mono": "JetBrains Mono"},
  "logo": {"asset_id": "0192f0c1-7d2e-7b3a-9c4d-5e6f7a8b9c0d", "width_mm": 35},
  "header": {"text": "Example Co", "show_logo": true},
  "footer": {"text": "Example Co · example.com", "legal": "Example Co S.L. · VAT ES00000000X",
             "page_numbers": true},
  "docx": {"font_fallbacks": {"heading": "Arial", "body": "Georgia", "mono": "Courier New"}}
}
```

Top-level fields: `id`, `name`, `colors`, `fonts`, `logo`, `header`,
`footer`, `docx`, and `spec_version` (set by Folio; keep it when copying a
brand from `get_brand`).

| field | rule | used for |
|---|---|---|
| `id` | slug `^[a-z0-9][a-z0-9-]{1,62}$`, unique | `brand_id` in templates and renders |
| `name` | required | shown in listings |
| `colors.primary` | `#RRGGBB` | headings, links, table rules, cover title |
| `colors.secondary` | `#RRGGBB` | available to layouts (dark accent) |
| `colors.accent` | `#RRGGBB` | available to layouts (highlights) |
| `colors.text` | `#RRGGBB` | body text |
| `colors.muted` | `#RRGGBB` | header, footer, secondary text, light rules |
| `colors.background` | `#RRGGBB` | page background (keep it light; it is also the reference for the blank-page and edge checks) |
| `fonts.heading`, `fonts.body`, `fonts.mono` | one of the bundled families | headings, body text, code |
| `logo.asset_id` | an uploaded, not deleted asset | cover and header, in PDF and DOCX |
| `logo.width_mm` | 5–120 | logo width on the cover (the header uses a fixed 7 mm height) |
| `header.text` | text | right side of the header from page 2 (every page without a cover) |
| `header.show_logo` | bool | logo on the left of the header |
| `footer.text` | text | first footer line |
| `footer.legal` | text, may be empty | second footer line |
| `footer.page_numbers` | bool | "Page N" (label in the document language) |
| `docx.font_fallbacks` | `heading`, `body`, `mono` → font names | fonts Word substitutes when the bundled font is not installed |

`id`, `name`, every colour and every font are required (validation lists
each problem with its path, e.g. `/colors/primary`). `logo` is optional, and
the `header`, `footer` and `docx` fields may be empty or omitted.

## Fonts

The bundled families are Inter (sans-serif), Source Serif 4 (serif),
JetBrains Mono (monospace) and Noto Sans (sans-serif with wide script
coverage); `get_capabilities.fonts` lists them. Custom fonts cannot be
uploaded. PDFs embed the fonts; DOCX files name them, and Word falls back to
`docx.font_fallbacks` on machines without them.

## Logos

Upload the logo with `upload_asset`. SVG gives the sharpest result (Folio
adds a PNG rendition for DOCX automatically; an SVG that rasterises beyond the pixel or byte limit is refused with `too_large`); PNG or JPEG of at least 600 px
width also work. SVGs with scripts, event handlers, `foreignObject` or
external references are refused. Deleting the asset later does not break
brand versions or documents that already use it, but new brand versions
cannot reference it.

## Versions and archiving

- `create_brand` creates version 1; `update_brand(id, expected_version, brand)`
  creates the next version when `expected_version` is the latest one,
  otherwise `conflict` with the latest version in the message.
- Renders always use the newest brand version; existing documents keep the
  version they were rendered with (shown in `get_document`), so updating a
  brand restyles only new renders and new revisions.
- `archive_brand(id, true)` hides the brand and blocks new renders and new
  templates with it; `archive_brand(id, false)` restores it.

## Checklist

1. Pick colours with enough contrast: `text` and `primary` on `background`.
2. Choose bundled fonts and DOCX fallbacks.
3. `upload_asset` the logo, then `create_brand`.
4. Render a template's sample with the new brand (for example
   `render_document` with `brand_id`, or a template that uses it plus
   `preview_template`) and look at the pages.
