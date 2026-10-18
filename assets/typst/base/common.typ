// SPDX-License-Identifier: AGPL-3.0-only
// Shared building blocks for Folio base layouts. Contract (documented in the
// typst-layouts guide): folio-layout(meta:, brand:, labels:, options:, body)
//   meta    effective metadata: title, date, language, author?, client?, project?,
//           reference?, version_label?, confidentiality?, plus template fields
//   brand   name, colors{primary,secondary,accent,text,muted,background},
//           fonts{heading,body,mono}, logo{path,width_mm}?, header{text,show_logo},
//           footer{text,legal,page_numbers}
//   labels  resolved label table for meta.language (e.g. labels.at("page"))
//   options cover, toc, page_size ("a4"|"letter"), heading_numbering

#let folio-math-font = "Noto Sans Math"

#let folio-get(d, key, default: none) = if key in d { d.at(key) } else { default }

#let folio-page-setup(meta, brand, labels, options, body) = {
  let primary = rgb(brand.colors.primary)
  let muted = rgb(brand.colors.muted)
  set document(title: meta.title, author: folio-get(meta, "author", default: ()))
  set page(
    paper: if options.page_size == "letter" { "us-letter" } else { "a4" },
    margin: (x: 20mm, top: 25mm, bottom: 22mm),
    fill: rgb(brand.colors.background),
    header: context {
      if counter(page).get().first() > 1 or not options.cover {
        set text(size: 8pt, fill: muted)
        grid(columns: (1fr, auto), align: (left + horizon, right + horizon),
          if brand.header.show_logo and folio-get(brand, "logo") != none {
            image("/" + brand.logo.path, height: 7mm)
          } else { [] },
          if brand.header.text != "" { brand.header.text } else { meta.title })
      }
    },
    footer: context {
      set text(size: 8pt, fill: muted)
      grid(columns: (1fr, auto), align: (left, right),
        [#brand.footer.text #if brand.footer.legal != "" [\ #brand.footer.legal]],
        if brand.footer.page_numbers [#labels.at("page") #counter(page).display()] else [])
    },
  )
  set text(font: (brand.fonts.body, "Noto Sans"), size: 10.5pt, fill: rgb(brand.colors.text), lang: meta.language)
  set par(justify: true, leading: 0.65em)
  show math.equation: set text(font: folio-math-font)
  show raw: set text(font: (brand.fonts.mono, "Noto Sans"), size: 9pt)
  show heading: set text(font: (brand.fonts.heading, "Noto Sans"), fill: primary, weight: "bold")
  show heading.where(level: 1): set text(size: 16pt)
  show heading.where(level: 2): set text(size: 13pt)
  set heading(numbering: if options.heading_numbering { "1.1" } else { none })
  show link: set text(fill: primary)
  // Header styling applies only to real header rows (table.header); header-less
  // tables (info, signatures) get plain rows. Cell text is never justified.
  // Pandoc emits table.header(...) + table.hline() only for tables with a head.
  // Such tables are rebuilt: horizontal rules only (0.7pt primary under the
  // header, 0.3pt muted under body rows), bold header cells. Header-less tables
  // get no rules. The stroke-none default doubles as the "already rebuilt" marker.
  set table(stroke: none, inset: (x: 5pt, y: 4pt))
  show table: it => if it.stroke != none { it } else {
    let f = it.fields()
    let kids = f.remove("children")
    let has-header = kids.any(c => c.func() == table.header)
    let _ = f.remove("stroke", default: none)
    kids = kids.filter(c => c.func() != table.hline).map(c => if c.func() == table.header {
      table.header(..c.children.map(x => if x.func() == table.cell {
        let g = x.fields()
        let b = g.remove("body")
        table.cell(..g, strong(b))
      } else { strong(x) }))
    } else { c })
    table(..f, ..kids, stroke: (x, y) => if not has-header { none } else if y == 0 {
      (bottom: 0.7pt + primary)
    } else { (bottom: 0.3pt + muted.lighten(60%)) })
  }
  show table.cell: set par(justify: false)
  set image(width: auto)
  body
}

#let folio-cover(meta, brand, labels) = page(header: none, footer: none)[
  #let primary = rgb(brand.colors.primary)
  #if folio-get(brand, "logo") != none { image("/" + brand.logo.path, width: brand.logo.width_mm * 1mm) }
  #v(1fr)
  #text(font: (brand.fonts.heading, "Noto Sans"), size: 28pt, weight: "bold", fill: primary)[#meta.title]
  #if folio-get(meta, "version_label") != none { v(4pt); text(size: 12pt, fill: rgb(brand.colors.muted))[#meta.version_label] }
  #v(16pt)
  #set text(size: 11pt)
  #if folio-get(meta, "client") != none [#labels.at("cover.prepared_for") #strong(meta.client) \ ]
  #if folio-get(meta, "project") != none [#meta.project \ ]
  #if folio-get(meta, "reference") != none [#meta.reference \ ]
  #meta.date
  #if folio-get(meta, "author") != none [ \ #meta.author]
  #v(2fr)
]

#let folio-toc(labels) = {
  outline(title: labels.at("toc.title"), depth: 3)
  pagebreak(weak: true)
}
