// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

type fakeRenderer struct{}

func (fakeRenderer) Render(_ context.Context, _ render.Request) (*render.Result, error) {
	return &render.Result{PDF: []byte("%PDF-fake"), DOCX: []byte("PK-fake"), Pages: [][]byte{[]byte("png1")}}, nil
}

type noAssets struct{}

func (noAssets) Asset(context.Context, string, bool) (model.ResolvedAsset, error) {
	return model.ResolvedAsset{}, model.ErrAssetNotFound
}

func newService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := store.NewBlobs(dir+"/blobs", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Blobs: blobs, Renderer: fakeRenderer{}, Assets: noAssets{}, Now: time.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}
}

func brand() model.Brand {
	return model.Brand{ID: "example", Name: "Example",
		Colors: model.BrandColors{Primary: "#1F4FD8", Secondary: "#0B1F44", Accent: "#F2A900", Text: "#1A1A1A", Muted: "#6B7280", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Source Serif 4", Mono: "JetBrains Mono"}}
}

func tmpl() model.Template {
	return model.Template{ID: "proposal", Name: "Proposal", BrandID: "example", Languages: []string{"en"},
		Layout:     model.Layout{Base: "proposal", Options: json.RawMessage(`{"cover":true}`)},
		Components: []string{"budget"}, Sections: []model.Section{{Title: "Budget", Component: "budget"}},
		Sample: model.Sample{Markdown: "# Budget\n", Metadata: json.RawMessage(`{"title":"T","date":"2026-10-18","language":"en"}`)}}
}

func TestConcurrentUpdateExactlyOneWins(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.CreateBrand(ctx, "alice", brand(), "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTemplate(ctx, "alice", tmpl(), "v1"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tp := tmpl()
			tp.Description = "edit"
			_, errs[i] = s.UpdateTemplate(ctx, "bob", "proposal", 1, tp, "v2")
		}(i)
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case IsConflict(err):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflicts != len(errs)-1 {
		t.Fatalf("ok=%d conflicts=%d", ok, conflicts)
	}
	it, _ := s.GetTemplate(ctx, "proposal", 0)
	if it.LatestVersion != 2 || len(it.Versions) != 2 {
		t.Fatalf("latest=%d versions=%d", it.LatestVersion, len(it.Versions))
	}
}

func TestArchiveBlocksResolve(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, _ = s.CreateBrand(ctx, "a", brand(), "")
	if _, err := s.CreateTemplate(ctx, "a", tmpl(), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveTemplate(ctx, "a", "proposal", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ResolveTemplate(ctx, "proposal", 0, false); err == nil {
		t.Fatal("archived template resolved for a new render")
	}
	items, _, _ := s.ListTemplates(ctx, ListFilter{})
	if len(items) != 0 {
		t.Fatal("archived template listed by default")
	}
	_ = s.ArchiveTemplate(ctx, "a", "proposal", false)
	if _, _, err := s.ResolveTemplate(ctx, "proposal", 0, false); err != nil {
		t.Fatal(err)
	}
}

func TestBrandValidation(t *testing.T) {
	s := newService(t)
	b := brand()
	b.Fonts.Body = "Comic Sans"
	b.Colors.Primary = "blue"
	if _, err := s.CreateBrand(context.Background(), "a", b, ""); err == nil {
		t.Fatal("invalid brand accepted")
	}
}

type jpegAssets struct{}

func (jpegAssets) Asset(context.Context, string, bool) (model.ResolvedAsset, error) {
	return model.ResolvedAsset{MIME: "image/jpeg"}, nil
}

func TestValidateBrandRejectsJPEGLogo(t *testing.T) {
	s := newService(t)
	s.Assets = jpegAssets{}
	b := brand()
	b.Logo = &model.BrandLogo{AssetID: "a1", WidthMM: 30}
	for _, f := range s.ValidateBrand(context.Background(), b) {
		if f.Path == "/logo/asset_id" {
			return
		}
	}
	t.Fatal("JPEG logo was not rejected")
}
