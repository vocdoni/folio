// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/ai"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
)

// Converter turns template-draft examples into model input (render.Engine).
type Converter interface {
	DOCXToMarkdown(ctx context.Context, docx []byte) (string, error)
	RasterizePDFPage(ctx context.Context, pdf []byte, page, ppi int) ([]byte, error)
}

// Example limits.
const (
	maxExamplePDFPages = 50
	examplePPI         = 96
)

// TemplateDraftIn is POST /ui/api/ai/template-draft.
type TemplateDraftIn struct {
	Description   string `json:"description,omitempty"`
	ExampleName   string `json:"example_name,omitempty"`
	ExampleBase64 string `json:"example_base64,omitempty"` // Markdown, plain text, DOCX or PDF
}

// TemplateDraftOut is the job result: the draft plus its sample preview.
type TemplateDraftOut struct {
	ai.Draft
	PreviewID string   `json:"preview_id,omitempty"`
	PageURLs  []string `json:"page_urls"`
	Pages     int      `json:"pages"`
}

type example struct {
	kind string // text | docx | pdf
	data []byte
}

func invalidf(format string, args ...any) error {
	return &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// readExample decodes and classifies the example file.
func (s *Server) readExample(in TemplateDraftIn) (*example, error) {
	if in.ExampleBase64 == "" {
		return nil, nil
	}
	maxB := s.o.Config.MaxAssetBytes
	if int64(base64.StdEncoding.DecodedLen(len(in.ExampleBase64))) > maxB+3 {
		return nil, &model.Error{Code: model.ErrTooLarge, Message: fmt.Sprintf("the example is larger than %d bytes", maxB)}
	}
	b, err := base64.StdEncoding.DecodeString(in.ExampleBase64)
	if err != nil {
		return nil, invalidf("example_base64 is not valid base64")
	}
	name := strings.ToLower(in.ExampleName)
	switch {
	case bytes.HasPrefix(b, []byte("%PDF-")):
		// The page count and encryption are read inside the job (convert),
		// under its time budget; the handler stays cheap.
		if !s.vision.Load() {
			return nil, invalidf("the configured model does not accept images, so PDF examples are off; attach Markdown or DOCX instead")
		}
		return &example{kind: "pdf", data: b}, nil
	case bytes.HasPrefix(b, []byte("PK\x03\x04")) && (strings.HasSuffix(name, ".docx") || bytes.Contains(b, []byte("word/document.xml"))):
		return &example{kind: "docx", data: b}, nil
	case utf8.Valid(b) && !bytes.ContainsRune(b, 0):
		return &example{kind: "text", data: b}, nil
	}
	return nil, invalidf("unsupported example: attach Markdown, plain text, DOCX or PDF")
}

// pdfInfo reads the page count and whether the PDF is encrypted.
func pdfInfo(ctx context.Context, b []byte) (pages int, encrypted bool, err error) {
	conf := pdfmodel.NewDefaultConfiguration()
	conf.ValidationMode = pdfmodel.ValidationRelaxed
	pc, err := api.ReadContext(ctx, bytes.NewReader(b), conf)
	if err == nil {
		err = api.ValidateContext(ctx, pc) // also sets the page count
	}
	if err != nil {
		return 0, false, err
	}
	return pc.PageCount, pc.Encrypt != nil, nil
}

// convert turns the example into text or page images under the preview
// queue (sandboxed pandoc and typst, render timeout). A PDF's page count
// and encryption are checked here, once, before any page is rasterised.
func (s *Server) convert(ctx context.Context, ex *example) (string, [][]byte, error) {
	if ex.kind == "text" {
		return string(ex.data), nil, nil
	}
	var n int
	if ex.kind == "pdf" {
		pages, encrypted, err := pdfInfo(ctx, ex.data)
		switch {
		case err != nil || encrypted:
			return "", nil, invalidf("the PDF cannot be read (it is encrypted or damaged); export an unprotected PDF")
		case pages > maxExamplePDFPages:
			return "", nil, invalidf("the PDF has %d pages; use one with at most %d", pages, maxExamplePDFPages)
		}
		n = pages
	}
	release, err := s.o.PreviewQueue.Acquire(ctx)
	if err != nil {
		return "", nil, err
	}
	defer release()
	if ex.kind == "docx" {
		md, err := s.o.Convert.DOCXToMarkdown(ctx, ex.data)
		if err != nil {
			return "", nil, invalidf("the DOCX could not be converted; save it again from your editor or attach Markdown")
		}
		return md, nil, nil
	}
	var pages [][]byte
	for p := 1; p <= min(n, ai.MaxExamplePages); p++ {
		img, err := s.o.Convert.RasterizePDFPage(ctx, ex.data, p, examplePPI)
		if err != nil {
			return "", nil, err
		}
		pages = append(pages, img)
	}
	return "", pages, nil
}

func (s *Server) handleTemplateDraft(w http.ResponseWriter, r *http.Request) {
	var in TemplateDraftIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if len(in.Description) > ai.MaxDescription {
		writeError(w, r, &model.Error{Code: model.ErrTooLarge, Message: fmt.Sprintf("the description is longer than %d KiB", ai.MaxDescription>>10)})
		return
	}
	ex, err := s.readExample(in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(in.Description) == "" && ex == nil {
		writeError(w, r, invalidf("describe the template or attach an example document"))
		return
	}
	session := sessionOf(r).key
	s.startJob(w, r, "template-draft", func(ctx context.Context, step func(string)) (any, error) {
		di := ai.DraftInput{Description: in.Description, Components: components.Names, Languages: brandkit.BuiltinLanguages}
		var err error
		if di.Layouts, err = bundled.BaseLayouts(); err != nil {
			return nil, err
		}
		if di.Brands, err = s.brandIDs(ctx); err != nil {
			return nil, err
		}
		if ex != nil {
			step("reading the example")
			if di.ExampleText, di.ExamplePages, err = s.convert(ctx, ex); err != nil {
				return nil, err
			}
		}
		var last PreviewOut
		check := func(ctx context.Context, t model.Template) ([]model.Finding, bool, error) {
			for {
				out, err := s.runPreview(ctx, session, t, previewPagesDef)
				if errors.Is(err, errPreviewInProgress) { // the studio is previewing too: wait for it
					select {
					case <-ctx.Done():
						return nil, false, ctx.Err()
					case <-time.After(500 * time.Millisecond):
					}
					continue
				}
				if err != nil {
					return nil, false, err
				}
				last = out
				return out.Findings, out.OK, nil
			}
		}
		d, err := s.o.AI.DraftTemplate(ctx, di, check, step)
		if err != nil {
			if len(di.ExamplePages) > 0 && strings.Contains(strings.ToLower(err.Error()), "image") {
				s.vision.Store(false) // the model refused image input: PDF examples are off from now on
			}
			return nil, err
		}
		out := TemplateDraftOut{Draft: *d, PageURLs: []string{}}
		if d.OK {
			out.PreviewID, out.PageURLs, out.Pages = last.PreviewID, last.PageURLs, last.Pages
		}
		return out, nil
	})
}

// brandIDs lists the live brands a draft may use.
func (s *Server) brandIDs(ctx context.Context) ([]string, error) {
	items, _, err := s.o.Catalog.ListBrands(ctx, catalog.ListFilter{Limit: 200})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	if len(out) == 0 {
		return nil, invalidf("create a brand first: every template uses one")
	}
	return out, nil
}
