// SPDX-License-Identifier: AGPL-3.0-only
// Base layout "proposal". See common.typ for the folio-layout contract.
#import "common.typ": folio-page-setup, folio-cover, folio-toc

#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = {
  show: folio-page-setup.with(meta, brand, labels, options)
  if options.cover { folio-cover(meta, brand, labels) }
  if options.toc { folio-toc(labels) }
  body
}
