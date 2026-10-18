// SPDX-License-Identifier: AGPL-3.0-only

// Package checks inspects rendered outputs: PDF validity and page count,
// blank pages and ink in the outer edge band of each PNG page, typst
// warnings, and DOCX structure.
package checks

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/vocdoni/folio/internal/model"
)

func init() {
	// pdfcpu would otherwise create a config directory under $HOME.
	pdfmodel.ConfigPath = "disable"
}

// PageOptions configure the raster checks. Bands are in millimetres at PPI.
type PageOptions struct {
	PPI            int     // resolution of the PNG pages (144)
	Background     [3]byte // brand background colour
	HeaderBandMM   float64 // excluded from the blank-page check
	FooterBandMM   float64
	EdgeBandMM     float64 // 2 mm
	BlankMaxInk    float64 // blank when the inked fraction of the content area is below this (0.0005)
	ColorTolerance int     // per-channel difference that counts as ink (24)
}

// DefaultPageOptions returns the documented defaults for a white background.
func DefaultPageOptions() PageOptions {
	return PageOptions{PPI: 144, Background: [3]byte{255, 255, 255}, HeaderBandMM: 20, FooterBandMM: 20,
		EdgeBandMM: 2, BlankMaxInk: 0.0005, ColorTolerance: 24}
}

func warn(code string, page int, msg, hint string) model.Finding {
	return model.Finding{Code: code, Severity: model.SeverityWarning, Page: page, Message: msg, Hint: hint}
}

// PDF validates the file with pdfcpu (relaxed mode) and checks the page
// count against maxPages. Invalid PDFs and too many pages are errors.
func PDF(ctx context.Context, pdf []byte, maxPages int) (int, []model.Finding) {
	conf := pdfmodel.NewDefaultConfiguration()
	conf.ValidationMode = pdfmodel.ValidationRelaxed
	pctx, err := api.ReadContext(ctx, bytes.NewReader(pdf), conf)
	if err == nil {
		err = api.ValidateContext(ctx, pctx)
	}
	if err != nil {
		return 0, []model.Finding{{Code: model.CodePDFInvalid, Severity: model.SeverityError, Message: "the rendered PDF failed validation: " + err.Error()}}
	}
	n := pctx.PageCount
	if maxPages > 0 && n > maxPages {
		return n, []model.Finding{{Code: model.CodeTooManyPages, Severity: model.SeverityError,
			Message: fmt.Sprintf("the document has %d pages; the limit is %d", n, maxPages), Hint: "shorten the document"}}
	}
	return n, nil
}

func inked(r, g, b uint32, bg [3]byte, tol int) bool {
	d := func(c uint32, ref byte) int {
		v := int(c>>8) - int(ref)
		if v < 0 {
			return -v
		}
		return v
	}
	return d(r, bg[0]) > tol || d(g, bg[1]) > tol || d(b, bg[2]) > tol
}

// Pages runs the blank-page and edge-ink checks on PNG pages (1-based page
// numbers in findings).
func Pages(pages [][]byte, o PageOptions) ([]model.Finding, error) {
	var out []model.Finding
	px := func(mm float64) int { return int(mm / 25.4 * float64(o.PPI)) }
	for i, raw := range pages {
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("checks: decode page %d: %w", i+1, err)
		}
		b := img.Bounds()
		edge := px(o.EdgeBandMM)
		top, bottom := b.Min.Y+px(o.HeaderBandMM), b.Max.Y-px(o.FooterBandMM)
		ink, area, edgeInk := 0, 0, false
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := img.At(x, y).RGBA()
				on := a > 0x7fff && inked(r, g, bl, o.Background, o.ColorTolerance)
				inEdge := x < b.Min.X+edge || x >= b.Max.X-edge || y < b.Min.Y+edge || y >= b.Max.Y-edge
				if inEdge && on {
					edgeInk = true
				}
				if y >= top && y < bottom {
					area++
					if on {
						ink++
					}
				}
			}
		}
		if edgeInk {
			out = append(out, warn(model.CodeEdgeInk, i+1, fmt.Sprintf("page %d has content within %g mm of the page edge (clipped or overflowing element)", i+1, o.EdgeBandMM),
				"shrink wide tables or images, or break long unbreakable text"))
		}
		if area > 0 && float64(ink)/float64(area) < o.BlankMaxInk {
			out = append(out, warn(model.CodeBlankPage, i+1, fmt.Sprintf("page %d is blank", i+1), "remove stray page breaks or empty sections"))
		}
	}
	return out, nil
}

// TypstWarnings turns typst warning lines into findings.
func TypstWarnings(lines []string) []model.Finding {
	out := make([]model.Finding, 0, len(lines))
	for _, l := range lines {
		out = append(out, warn(model.CodeTypstWarning, 0, l, ""))
	}
	return out
}

// DOCX checks the file is a zip with [Content_Types].xml and a parseable
// word/document.xml.
func DOCX(docx []byte) []model.Finding {
	bad := func(msg string) []model.Finding {
		return []model.Finding{{Code: model.CodeDOCXInvalid, Severity: model.SeverityError, Message: msg}}
	}
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		return bad("the rendered DOCX is not a valid zip archive")
	}
	var ct, doc bool
	for _, f := range zr.File {
		switch f.Name {
		case "[Content_Types].xml":
			ct = true
		case "word/document.xml":
			rc, err := f.Open()
			if err != nil {
				return bad("word/document.xml cannot be opened")
			}
			dec := xml.NewDecoder(io.LimitReader(rc, 64<<20))
			for {
				if _, err := dec.Token(); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					rc.Close()
					return bad("word/document.xml is not well-formed XML")
				}
			}
			rc.Close()
			doc = true
		}
	}
	if !ct || !doc {
		return bad("the rendered DOCX lacks [Content_Types].xml or word/document.xml")
	}
	return nil
}
