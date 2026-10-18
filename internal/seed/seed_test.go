// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// realEnv is a database with a catalog over the real render pipeline.
func realEnv(t *testing.T) (*store.DB, *catalog.Service, *assets.Service) {
	t.Helper()
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
	t.Cleanup(func() { db.Close() })
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
	return db, cat, as
}

// TestApplySeedsThroughCatalog runs the real pipeline: every starter
// template's sample renders with no warnings other than placeholders.
func TestApplySeedsThroughCatalog(t *testing.T) {
	ctx := context.Background()
	db, cat, as := realEnv(t)
	res, err := Apply(ctx, db, cat, as, time.Now(), quiet())
	if err != nil || !res.Seeded {
		t.Fatalf("%+v %v", res, err)
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

// TestUpgradeFromSeedV1 (A-AC2, F2): a database seeded with the v1 starters
// restarts with seed v2. The starters whose spec changed get version 2 by
// folio through the real catalog (validation and sample render); a starter
// whose latest version alice created stays at her version and is logged; the
// unchanged starters and the brand keep version 1 and the logo asset; and
// seed_version becomes 2.
func TestUpgradeFromSeedV1(t *testing.T) {
	ctx := context.Background()
	db, cat, as := realEnv(t)
	seedV1(t, db, cat, as, "alice")
	logoBefore, _, err := cat.ResolveBrand(ctx, "example", 0)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	res, err := Apply(ctx, db, cat, as, time.Now(), slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Upgraded, ",") != "template:proposal,template:sow" || strings.Join(res.Skipped, ",") != "template:quote" ||
		len(res.Created) != 0 || res.Seeded {
		t.Fatalf("result = %+v", res)
	}
	want := map[string][2]any{"proposal": {2, Actor}, "sow": {2, Actor}, "quote": {2, "alice"},
		"requirements": {1, Actor}, "technical-report": {1, Actor}}
	for id, w := range want {
		it, err := cat.GetTemplate(ctx, id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if it.LatestVersion != w[0] || latestBy(it) != w[1] {
			t.Errorf("%s: v%d by %s, want v%d by %s", id, it.LatestVersion, latestBy(it), w[0], w[1])
		}
	}
	it, _ := cat.GetTemplate(ctx, "proposal", 0)
	if n := it.Versions[1].Note; n != "starter upgrade (seed v2)" {
		t.Errorf("note = %q", n)
	}
	if v1, err := cat.GetTemplate(ctx, "proposal", 1); err != nil || !strings.Contains(string(v1.Spec), "payment_pct") {
		t.Errorf("version 1 must stay readable: %v", err)
	}
	b, bv, err := cat.ResolveBrand(ctx, "example", 0)
	if err != nil || bv != 1 || b.Logo.AssetID != logoBefore.Logo.AssetID {
		t.Fatalf("brand: v%d logo %s (was %s) %v", bv, b.Logo.AssetID, logoBefore.Logo.AssetID, err)
	}
	if v := meta(t, db, VersionKey); v != "2" {
		t.Fatalf("seed_version = %q", v)
	}
	l := logs.String()
	if !strings.Contains(l, "starter upgrade skipped") || !strings.Contains(l, "id=quote") || !strings.Contains(l, "created_by=alice") ||
		!strings.Contains(l, "seed_version=2") || !strings.Contains(l, "msg=\"starter upgrade\" kind=template id=proposal version=2") {
		t.Fatalf("logs:\n%s", l)
	}
	if res, err := Apply(ctx, db, cat, as, time.Now(), quiet()); err != nil || len(res.Upgraded)+len(res.Skipped) != 0 {
		t.Fatalf("a second start must not upgrade again: %+v %v", res, err)
	}
}
