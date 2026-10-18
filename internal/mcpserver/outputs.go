// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/checks"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
)

// FileOut is a downloadable output in tool results.
type FileOut struct {
	Kind      string    `json:"kind" jsonschema:"pdf, docx or preview"`
	Name      string    `json:"name"`
	URL       string    `json:"url" jsonschema:"public download URL (a bearer capability: anyone with it can download)"`
	MIME      string    `json:"mime"`
	Size      int       `json:"size"`
	SHA256    string    `json:"sha256"`
	Page      int       `json:"page,omitempty" jsonschema:"page number of a preview PNG"`
	Pages     int       `json:"pages,omitempty" jsonschema:"page count of a PDF"`
	ExpiresAt time.Time `json:"expires_at,omitzero" jsonschema:"template previews expire; document files do not"`
}

// fileURL is the public download URL of a stored file.
func (d *Deps) fileURL(f catalog.File) string {
	return strings.TrimRight(d.Config.PublicURL, "/") + "/f/" + f.ID + "/" + f.Name
}

// Preview modes of include_previews.
const (
	previewsNone  = "none"
	previewsFirst = "first"
	previewsAll   = "all"
)

func previewMode(s string) (string, error) {
	switch s {
	case "":
		return previewsFirst, nil
	case previewsNone, previewsFirst, previewsAll:
		return s, nil
	}
	return "", invalid("include_previews must be none, first or all (got %q)", s)
}

// outputs splits stored files into documents (pdf, docx; with the PDF page
// count) and preview URLs, and attaches one resource_link per document file.
func (c *call) outputs(files []catalog.File) (docs []FileOut, previews []string, pages int) {
	for _, f := range files {
		out := FileOut{Kind: f.Kind, Name: f.Name, URL: c.d.fileURL(f), MIME: f.MIME, Size: f.Size, SHA256: f.SHA256, Page: f.Page, ExpiresAt: f.ExpiresAt}
		if f.Kind == "preview" {
			previews = append(previews, out.URL)
			continue
		}
		if f.Kind == "pdf" {
			out.Pages = c.pdfPages(f)
			pages = out.Pages
		}
		docs = append(docs, out)
		size := int64(f.Size)
		c.Add(&mcp.ResourceLink{URI: out.URL, Name: f.Name, Title: f.Kind, MIMEType: f.MIME, Size: &size})
	}
	return docs, previews, pages
}

// pdfPages reads the stored PDF and counts its pages (0 when unreadable).
func (c *call) pdfPages(f catalog.File) int {
	data, err := c.d.Blobs.Get(f.SHA256)
	if err != nil {
		return 0
	}
	n, _ := checks.PDF(context.Background(), data, math.MaxInt32)
	return n
}

// pagePNGs returns full-resolution PNGs of the requested pages: stored
// previews first, other pages rasterised from the stored PDF (taking a render
// slot, so on-demand previews obey the render concurrency limit).
func (c *call) pagePNGs(ctx context.Context, files []catalog.File, pages []int) (map[int][]byte, error) {
	out := map[int][]byte{}
	var pdf *catalog.File
	for i := range files {
		f := files[i]
		switch {
		case f.Kind == "pdf":
			pdf = &files[i]
		case f.Kind == "preview" && slices.Contains(pages, f.Page):
			data, err := c.d.Blobs.Get(f.SHA256)
			if err != nil {
				return nil, fmt.Errorf("mcpserver: preview blob: %w", err)
			}
			out[f.Page] = data
		}
	}
	var missing []int
	for _, p := range pages {
		if _, ok := out[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	if pdf == nil {
		return nil, &model.Error{Code: model.ErrNotFound,
			Message: fmt.Sprintf("pages %v are not stored and this revision has no PDF to render them from (it was rendered with formats=[docx])", missing)}
	}
	data, err := c.d.Blobs.Get(pdf.SHA256)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: pdf blob: %w", err)
	}
	release, err := c.d.Queue.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	for _, p := range missing {
		img, err := c.d.Raster.RasterizePDFPage(ctx, data, p, render.PreviewPPI)
		if err != nil {
			return nil, err
		}
		out[p] = img
	}
	return out, nil
}

// attachPages adds the given pages (at most MaxImages) as image content.
func (c *call) attachPages(ctx context.Context, files []catalog.File, pages []int) error {
	if len(pages) > MaxImages {
		pages = pages[:MaxImages]
	}
	if len(pages) == 0 {
		return nil
	}
	imgs, err := c.pagePNGs(ctx, files, pages)
	if err != nil {
		return err
	}
	for _, p := range pages {
		ic, err := imageContent(imgs[p])
		if err != nil {
			return err
		}
		c.Add(ic)
	}
	return nil
}

// attachPreviews adds preview images for include_previews mode (first: page
// 1; all: up to MaxImages pages of the PDF).
func (c *call) attachPreviews(ctx context.Context, files []catalog.File, mode string, pageCount int) error {
	var pages []int
	switch mode {
	case previewsFirst:
		pages = []int{1}
	case previewsAll:
		for p := 1; p <= min(max(pageCount, 1), MaxImages); p++ {
			pages = append(pages, p)
		}
	}
	return c.attachPages(ctx, files, pages)
}

// listLimit validates limit (default 50, max 200).
func listLimit(n int) (int, error) {
	switch {
	case n == 0:
		return 50, nil
	case n < 0 || n > 200:
		return 0, invalid("limit must be between 1 and 200")
	}
	return n, nil
}
