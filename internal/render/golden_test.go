// SPDX-License-Identifier: AGPL-3.0-only

package render_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/sandbox"
	"github.com/vocdoni/folio/internal/seed"
	"github.com/vocdoni/folio/internal/validate"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.json")

type goldenAssets struct{ logo model.ResolvedAsset }

func (g goldenAssets) Asset(_ context.Context, id string, _ bool) (model.ResolvedAsset, error) {
	if id == g.logo.ID {
		return g.logo, nil
	}
	return model.ResolvedAsset{}, model.ErrAssetNotFound
}

// TestGoldenStarterTemplates (AC1): each starter sample renders twice to
// identical PDF and DOCX bytes that match testdata/golden.json.
func TestGoldenStarterTemplates(t *testing.T) {
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		t.Skipf("SKIPPING: %v — set FOLIO_PANDOC to pandoc 3.12.1", err)
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		t.Skipf("SKIPPING: %v — set FOLIO_TYPST to typst 0.15.1", err)
	}
	rt, err := bundled.Materialize(filepath.Join(os.TempDir(), "folio-test-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	l := sandbox.DefaultLimits()
	l.Timeout = 60 * time.Second
	e, err := render.NewEngine(render.EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Self: self, JobRoot: t.TempDir(), Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svg := seed.Logo()
	png, err := e.SVGToPNG(ctx, svg)
	if err != nil {
		t.Fatal(err)
	}
	p := render.NewPipeline(render.PipelineConfig{Engine: e, Queue: render.NewQueue(2, 8),
		Assets:   goldenAssets{logo: model.ResolvedAsset{ID: seed.LogoPlaceholderID, MIME: "image/svg+xml", Data: svg, PNG: png, Width: 240, Height: 64}},
		Limits:   validate.Limits{MaxMarkdownBytes: 1 << 20, MaxNesting: 32, MaxTableCells: 10000},
		MaxPages: 200})
	brand, err := seed.Brand()
	if err != nil {
		t.Fatal(err)
	}
	templates, err := seed.Templates()
	if err != nil {
		t.Fatal(err)
	}
	type sums struct {
		PDF  string `json:"pdf"`
		DOCX string `json:"docx"`
	}
	got := map[string]sums{}
	for _, tpl := range templates {
		req := render.Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1,
			Input: model.DocumentInput{Markdown: tpl.Sample.Markdown, Metadata: tpl.Sample.Metadata, Data: tpl.Sample.Data}}
		var runs [2]sums
		for i := range runs {
			res, err := p.Render(ctx, req)
			if err != nil {
				t.Fatalf("%s: %v", tpl.ID, err)
			}
			for _, w := range res.Warnings {
				if w.Code != model.CodePlaceholder {
					t.Errorf("%s: warning %+v", tpl.ID, w)
				}
			}
			ps, ds := sha256.Sum256(res.PDF), sha256.Sum256(res.DOCX)
			runs[i] = sums{hex.EncodeToString(ps[:]), hex.EncodeToString(ds[:])}
		}
		if runs[0] != runs[1] {
			t.Fatalf("%s: renders differ: %+v", tpl.ID, runs)
		}
		got[tpl.ID] = runs[0]
	}
	path := filepath.Join("testdata", "golden.json")
	if *update {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	var want map[string]sums
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for id, s := range got {
		if want[id] != s {
			t.Errorf("%s: got %+v, golden %+v (output changed: if intended, run -update and note it in the CHANGELOG)", id, s, want[id])
		}
	}
	if len(want) != len(got) {
		t.Errorf("golden has %d templates, rendered %d", len(want), len(got))
	}
}
