// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

func testPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if y > h/3 && y < h/2 {
				c = color.RGBA{200, 36, 107, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// fakeRenderer stands in for render.Pipeline: a real two-page PDF, one PNG
// per page, a validation failure for Markdown containing FAIL.
type fakeRenderer struct {
	pdf, page []byte
	calls     atomic.Int32
	gate      chan struct{} // when set, Render waits for it
	formats   atomic.Value  // []model.Format of the last call
}

func newFakeRenderer(t *testing.T) *fakeRenderer {
	t.Helper()
	pdf, err := os.ReadFile(filepath.Join("..", "checks", "testdata", "two-pages.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRenderer{pdf: pdf, page: testPNG(124, 175)}
}

func (f *fakeRenderer) Render(ctx context.Context, req render.Request) (*render.Result, error) {
	f.calls.Add(1)
	f.formats.Store(req.Input.Formats)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if strings.Contains(req.Input.Markdown, "FAIL") {
		return nil, &model.Error{Code: model.ErrValidationFailed, Message: "1 problem(s) must be fixed before rendering; see details",
			Details: []model.Finding{{Code: model.CodeMissingSection, Severity: model.SeverityError, Path: "/markdown", Message: `required section "Scope" is missing`}}}
	}
	canon, sum, err := render.CanonicalInput(model.Ref{ID: req.Template.ID, Version: req.TemplateVersion},
		model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, req.Input)
	if err != nil {
		return nil, err
	}
	res := &render.Result{Canonical: canon, InputSHA256: sum, Warnings: []model.Finding{}, PageCount: 2,
		PDF: f.pdf, Pages: [][]byte{f.page, f.page}}
	wantDOCX := len(req.Input.Formats) == 0
	for _, x := range req.Input.Formats {
		wantDOCX = wantDOCX || x == model.FormatDOCX
	}
	if wantDOCX {
		res.DOCX = []byte("PK\x03\x04docx-" + sum)
	}
	return res, nil
}

type fakeRaster struct{ calls atomic.Int32 }

func (r *fakeRaster) RasterizePDFPage(_ context.Context, _ []byte, page, _ int) ([]byte, error) {
	r.calls.Add(1)
	return testPNG(124+page, 175), nil
}

func (r *fakeRaster) SVGToPNG(context.Context, []byte) ([]byte, error) { return testPNG(200, 60), nil }

type svc struct {
	*fixture
	r      *fakeRenderer
	raster *fakeRaster
	blobs  *store.Blobs
}

// newServices wires the real catalog, documents and assets services over the
// fixture's database with the fake renderer, then signs in.
func newServices(t *testing.T, tweak func(*Options)) *svc {
	t.Helper()
	sv := &svc{r: newFakeRenderer(t), raster: &fakeRaster{}}
	f := newFixture(t, func(o *Options) {
		blobs, err := store.NewBlobs(filepath.Join(t.TempDir(), "blobs"), o.Now)
		if err != nil {
			t.Fatal(err)
		}
		sv.blobs = blobs
		as := &assets.Service{DB: o.DB, Blobs: blobs, Fetcher: assets.NewFetcher(1 << 20), Raster: sv.raster, MaxBytes: 1 << 20, Now: o.Now}
		cat := &catalog.Service{DB: o.DB, Blobs: blobs, Renderer: sv.r, Assets: as, PackagesDir: t.TempDir(), Now: o.Now,
			PreviewTTL: 24 * 3600e9, PreviewPagesMax: 1}
		docs := &documents.Service{DB: o.DB, Blobs: blobs, Renderer: sv.r, Resolver: cat, Now: o.Now, PreviewPages: 1, GCGrace: 3600e9}
		o.Blobs, o.Catalog, o.Documents, o.Assets, o.Validator, o.Raster = blobs, cat, docs, as, sv.r, sv.raster
		o.Queue = render.NewQueue(1, 2)
		if tweak != nil {
			tweak(o)
		}
	})
	sv.fixture = f
	f.login()
	return sv
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder, want int) T {
	t.Helper()
	var v T
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body)
	}
	return v
}

func brandSpec(id string) model.Brand {
	return model.Brand{ID: id, Name: "Acme", Colors: model.BrandColors{Primary: "#1F4FD8", Secondary: "#0B1F44", Accent: "#F2A900",
		Text: "#1A1A1A", Muted: "#6B7280", Background: "#FFFFFF"}, Fonts: model.BrandFonts{Heading: "Inter", Body: "Source Serif 4", Mono: "JetBrains Mono"},
		Header: model.BrandHeader{Text: "Acme"}, Footer: model.BrandFooter{Text: "Acme", PageNumbers: true}}
}

func templateSpec(id, brand string) model.Template {
	return model.Template{ID: id, Name: "Memo", Description: "Short memo.", Category: "memo", BrandID: brand, Languages: []string{"en"},
		Layout: model.Layout{Base: "report"}, Sections: []model.Section{{Title: "Summary", Required: true}}, Components: []string{},
		BodySkeleton: "# Summary\n\n", Sample: model.Sample{Markdown: "# Summary\n\nAll good.",
			Metadata: json.RawMessage(`{"title":"Memo","date":"2026-10-18","language":"en"}`)}}
}

// TestCatalogParity (B-AC4): brands and templates through the UI API, with
// the expected_version conflict.
func TestCatalogParity(t *testing.T) {
	sv := newServices(t, nil)
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	b2 := brandSpec("acme")
	b2.Name = "Acme Corp"
	decode[map[string]any](t, sv.do("PUT", "/ui/api/brands/acme", BrandIn{Brand: b2, ExpectedVersion: 1, Note: "rename"}, nil), 200)
	if w := sv.do("PUT", "/ui/api/brands/acme", BrandIn{Brand: b2, ExpectedVersion: 1}, nil); w.Code != http.StatusConflict || errorCode(t, w) != "conflict" {
		t.Fatalf("stale brand update: %d %s", w.Code, w.Body)
	}
	bad := brandSpec("bad")
	bad.Colors.Primary = "blue"
	if w := sv.do("POST", "/ui/api/brands", BrandIn{Brand: bad}, nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "/colors/primary") {
		t.Fatalf("invalid brand: %d %s", w.Code, w.Body)
	}
	brand := decode[ItemOut](t, sv.do("GET", "/ui/api/brands/acme", nil, nil), 200)
	if brand.Version != 2 || len(brand.Versions) != 2 || brand.Versions[1].CreatedBy != Actor {
		t.Fatalf("brand %+v", brand)
	}
	saved := decode[TemplateSavedOut](t, sv.do("POST", "/ui/api/templates", TemplateIn{Template: templateSpec("memo", "acme")}, nil), 200)
	if saved.Version != 1 || len(saved.Files) == 0 || !strings.HasPrefix(saved.Files[0].URL, "https://folio.example.test/f/") {
		t.Fatalf("created %+v", saved)
	}
	t2 := templateSpec("memo", "acme")
	t2.Description = "Updated."
	decode[TemplateSavedOut](t, sv.do("PUT", "/ui/api/templates/memo", TemplateIn{Template: t2, ExpectedVersion: 1}, nil), 200)
	w := sv.do("PUT", "/ui/api/templates/memo", TemplateIn{Template: t2, ExpectedVersion: 1}, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "expected_version=2") {
		t.Fatalf("stale template update: %d %s", w.Code, w.Body)
	}
	list := decode[TemplateListOut](t, sv.do("GET", "/ui/api/templates", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].Version != 2 || list.Items[0].Sections[0].Title != "Summary" {
		t.Fatalf("list %+v", list)
	}
	tpl := decode[ItemOut](t, sv.do("GET", "/ui/api/templates/memo?version=1", nil, nil), 200)
	if tpl.Version != 1 || tpl.LatestVersion != 2 || !strings.Contains(string(tpl.CoreMetadataSchema), `"language"`) {
		t.Fatalf("get v1 %+v", tpl)
	}
	decode[map[string]any](t, sv.do("POST", "/ui/api/templates/memo/archive", ArchiveIn{Archived: true}, nil), 200)
	if list := decode[TemplateListOut](t, sv.do("GET", "/ui/api/templates", nil, nil), 200); len(list.Items) != 0 {
		t.Fatal("archived template listed")
	}
	if list := decode[TemplateListOut](t, sv.do("GET", "/ui/api/templates?include_archived=true", nil, nil), 200); len(list.Items) != 1 || !list.Items[0].Archived {
		t.Fatal("include_archived")
	}
}

// TestDocumentsParity (B-AC4): render (idempotent), list with stored
// previews, detail, pages (stored and on demand), publish, trash, restore,
// purge.
func TestDocumentsParity(t *testing.T) {
	sv := newServices(t, nil)
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	decode[TemplateSavedOut](t, sv.do("POST", "/ui/api/templates", TemplateIn{Template: templateSpec("memo", "acme")}, nil), 200)
	in := RenderIn{TemplateID: "memo", Markdown: "# Summary\n\nHello.", Metadata: json.RawMessage(`{"title":"Hello memo","date":"2026-10-18","language":"en","client":"ACME"}`),
		Formats: []model.Format{model.FormatPDF}}
	if w := sv.do("POST", "/ui/api/render", in, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("render without idempotency key: %d", w.Code)
	}
	in.IdempotencyKey = "6f1c2d3e-0000-4000-8000-000000000001"
	out := decode[RenderOut](t, sv.do("POST", "/ui/api/render", in, nil), 200)
	again := decode[RenderOut](t, sv.do("POST", "/ui/api/render", in, nil), 200)
	if out.Revision != 1 || out.DocumentID == "" || again.DocumentID != out.DocumentID || !again.Unchanged || sv.r.calls.Load() != 2 {
		t.Fatalf("render %+v again %+v calls %d", out, again, sv.r.calls.Load())
	}
	for _, f := range out.Files {
		if f.Kind == "docx" {
			t.Fatal("formats [pdf] must not produce a DOCX")
		}
	}
	fail := in
	fail.Markdown, fail.IdempotencyKey = "FAIL", "6f1c2d3e-0000-4000-8000-000000000002"
	if w := sv.do("POST", "/ui/api/render", fail, nil); w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "validation_failed" {
		t.Fatalf("invalid render: %d %s", w.Code, w.Body)
	}
	list := decode[DocumentListOut](t, sv.do("GET", "/ui/api/documents?query=hello", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].PreviewURL == "" || list.Items[0].Client != "ACME" {
		t.Fatalf("list %+v", list)
	}
	if w := sv.do("GET", list.Items[0].PreviewURL, nil, nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("stored preview: %d", w.Code)
	}
	doc := decode[DocumentOut](t, sv.do("GET", "/ui/api/documents/"+out.DocumentID, nil, nil), 200)
	if doc.PageCount != 2 || len(doc.PageURLs) != 2 || doc.Status != "draft" {
		t.Fatalf("doc %+v", doc)
	}
	if w := sv.do("GET", doc.PageURLs[1]+"&stored=true", nil, nil); w.Code != http.StatusNotFound || sv.raster.calls.Load() != 0 {
		t.Fatalf("stored=true must never rasterise: %d", w.Code)
	}
	for i := 0; i < 2; i++ { // second request hits the LRU
		if w := sv.do("GET", doc.PageURLs[1], nil, nil); w.Code != 200 {
			t.Fatalf("page 2: %d %s", w.Code, w.Body)
		}
	}
	if sv.raster.calls.Load() != 1 {
		t.Fatalf("rasterised %d times, want 1 (LRU)", sv.raster.calls.Load())
	}
	if w := sv.do("GET", "/ui/api/documents/"+out.DocumentID+"/pages/9.png", nil, nil); w.Code != 404 {
		t.Fatalf("page beyond the end: %d", w.Code)
	}
	src := decode[SourceOut](t, sv.do("GET", "/ui/api/documents/"+out.DocumentID+"/source", nil, nil), 200)
	if src.Markdown != in.Markdown || src.TemplateID != "memo" {
		t.Fatalf("source %+v", src)
	}
	rev := 1
	next := RenderIn{TemplateID: "memo", Markdown: "# Summary\n\nHello again.", Metadata: in.Metadata, DocumentID: out.DocumentID, ExpectedRevision: &rev}
	if r2 := decode[RenderOut](t, sv.do("POST", "/ui/api/render", next, nil), 200); r2.Revision != 2 {
		t.Fatalf("revision %+v", r2)
	}
	decode[StatusOut](t, sv.do("POST", "/ui/api/documents/"+out.DocumentID+"/publish", PublishIn{Revision: 2}, nil), 200)
	if w := sv.do("DELETE", "/ui/api/documents/"+out.DocumentID, nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("trash published without confirmation: %d", w.Code)
	}
	if st := decode[StatusOut](t, sv.do("DELETE", "/ui/api/documents/"+out.DocumentID+"?confirm_published=true", nil, nil), 200); st.Status != "trashed" {
		t.Fatalf("trash %+v", st)
	}
	if w := sv.do("GET", doc.PageURLs[0], nil, nil); w.Code != 200 {
		t.Fatalf("pages of trashed documents stay visible in the UI: %d", w.Code)
	}
	if st := decode[StatusOut](t, sv.do("POST", "/ui/api/documents/"+out.DocumentID+"/restore", nil, nil), 200); st.Status != "published" {
		t.Fatalf("restore %+v", st)
	}
	if st := decode[StatusOut](t, sv.do("DELETE", "/ui/api/documents/"+out.DocumentID+"?purge=true&confirm_published=true", nil, nil), 200); st.Status != "purged" {
		t.Fatalf("purge %+v", st)
	}
	if w := sv.do("GET", "/ui/api/documents/"+out.DocumentID, nil, nil); w.Code != 404 {
		t.Fatalf("purged: %d", w.Code)
	}
}

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="40"><rect width="120" height="40" fill="#C8246B"/></svg>`

func (sv *svc) upload(name string, data []byte) *httptest.ResponseRecorder {
	sv.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.WriteField("description", "Logo")
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/ui/api/assets", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(HeaderUI, "1")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: sv.cookie})
	w := httptest.NewRecorder()
	sv.s.ServeHTTP(w, req)
	return w
}

// TestAssetsParity (B-AC4): multipart upload, list, content with a
// sandboxing CSP, delete, size limit.
func TestAssetsParity(t *testing.T) {
	sv := newServices(t, nil)
	a := decode[AssetOut](t, sv.upload("logo.svg", []byte(testSVG)), 200)
	if a.MIME != "image/svg+xml" || a.Name != "logo.svg" || a.URL == "" || a.Description != "Logo" {
		t.Fatalf("upload %+v", a)
	}
	w := sv.do("GET", a.URL, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("content: %d %v", w.Code, w.Header())
	}
	if w := sv.upload("big.png", bytes.Repeat([]byte{1}, 1<<20+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: %d %s", w.Code, w.Body)
	}
	if w := sv.upload("x.txt", []byte("hello")); w.Code != http.StatusBadRequest {
		t.Fatalf("not an image: %d", w.Code)
	}
	list := decode[struct {
		Items []AssetOut `json:"items"`
	}](t, sv.do("GET", "/ui/api/assets", nil, nil), 200)
	if len(list.Items) != 1 {
		t.Fatalf("list %+v", list)
	}
	decode[map[string]any](t, sv.do("DELETE", "/ui/api/assets/"+a.ID, nil, nil), 200)
	if w := sv.do("DELETE", "/ui/api/assets/"+a.ID, nil, nil); w.Code != 404 {
		t.Fatalf("second delete: %d", w.Code)
	}
}
