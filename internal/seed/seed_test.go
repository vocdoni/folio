// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/sandbox"
	"github.com/vocdoni/folio/internal/store"
	"github.com/vocdoni/folio/internal/validate"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestSeedContentIsWellFormed(t *testing.T) {
	b, err := Brand()
	if err != nil || b.Logo == nil || b.Logo.AssetID != LogoPlaceholderID {
		t.Fatalf("brand: %+v %v", b, err)
	}
	if err := assets.CheckSVG(Logo()); err != nil {
		t.Fatalf("logo: %v", err)
	}
	ts, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	cat := &catalog.Service{}
	for _, tp := range ts {
		if fs := cat.ValidateTemplate(tp); model.HasErrors(fs) {
			t.Errorf("%s: %+v", tp.ID, fs)
		}
		if !slices.Equal(tp.Languages, []string{"en", "es", "ca"}) || tp.BrandID != "example" {
			t.Errorf("%s: languages %v brand %s", tp.ID, tp.Languages, tp.BrandID)
		}
		var meta struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(tp.Sample.Metadata, &meta); err != nil || meta.Language != "en" {
			t.Errorf("%s: sample must be in en", tp.ID)
		}
	}
}

// TestApplySeedsThroughCatalog runs the real pipeline: every starter
// template's sample renders with no warnings other than placeholders.
func TestApplySeedsThroughCatalog(t *testing.T) {
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		t.Skipf("SKIPPING: %v (set FOLIO_PANDOC)", err)
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		t.Skipf("SKIPPING: %v (set FOLIO_TYPST)", err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, _ := store.NewBlobs(filepath.Join(dir, "blobs"), time.Now)
	rt, err := bundled.Materialize(filepath.Join(os.TempDir(), "folio-test-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	e, err := render.NewEngine(render.EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Self: self, JobRoot: t.TempDir(), Limits: sandbox.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	as := &assets.Service{DB: db, Blobs: blobs, Fetcher: assets.NewFetcher(1 << 20), Raster: e, MaxBytes: 10 << 20, Now: time.Now}
	p := render.NewPipeline(render.PipelineConfig{Engine: e, Queue: render.NewQueue(2, 8), Assets: as,
		Limits: validate.Limits{MaxMarkdownBytes: 1 << 20, MaxNesting: 32, MaxTableCells: 10000}, MaxPages: 200})
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: p, Assets: as, PackagesDir: rt.PackagesDir, Now: time.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}
	if err := Apply(ctx, cat, as); err != nil {
		t.Fatal(err)
	}
	b, _, err := cat.ResolveBrand(ctx, "example", 0)
	if err != nil || b.Logo.AssetID == LogoPlaceholderID {
		t.Fatalf("brand logo not replaced: %+v %v", b.Logo, err)
	}
	for _, id := range TemplateIDs {
		if _, v, err := cat.ResolveTemplate(ctx, id, 0, false); err != nil || v != 1 {
			t.Errorf("template %s: v%d %v", id, v, err)
		}
	}
}
