// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/sandbox"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestRenderExampleRequest(t *testing.T) {
	for _, name := range []string{"pandoc", "typst"} {
		if _, err := sandbox.LookupTool(name); err != nil {
			t.Skipf("SKIPPING: %v (set FOLIO_PANDOC / FOLIO_TYPST)", err)
		}
	}
	cfg, err := config.Load(os.LookupEnv, config.ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	rt := filepath.Join(os.TempDir(), "folio-test-runtime")
	if err := runRender(context.Background(), cfg, filepath.Join("..", "..", "examples", "render-request.json"), out, rt); err != nil {
		t.Fatal(err)
	}
	pdf, err := os.ReadFile(filepath.Join(out, "document.pdf"))
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("document.pdf: %v", err)
	}
	for _, f := range []string{"document.docx", "page-1.png", "result.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	var res struct {
		Computed struct {
			Budget struct {
				Total string `json:"total"`
			} `json:"budget"`
		} `json:"computed"`
		Warnings []any `json:"warnings"`
	}
	raw, _ := os.ReadFile(filepath.Join(out, "result.json"))
	if err := json.Unmarshal(raw, &res); err != nil || res.Computed.Budget.Total != "22118.80" || len(res.Warnings) != 0 {
		t.Fatalf("result.json = %s", raw)
	}
}

func TestRenderRejectsUnknownRequestFields(t *testing.T) {
	cfg, _ := config.Load(os.LookupEnv, config.ModeRender)
	dir := t.TempDir()
	p := filepath.Join(dir, "req.json")
	if err := os.WriteFile(p, []byte(`{"template": {}, "brand": {}, "document": {}, "bogus": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRender(context.Background(), cfg, p, filepath.Join(dir, "out"), filepath.Join(dir, "rt")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

// An SVG that passes the sanitiser but declares a huge canvas must not produce
// an unbounded PNG rendition: 2300 px at 300 ppi rasterises to ~51.6 MP.
func TestDirAssetsRejectsOversizedSVGRendition(t *testing.T) {
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		t.Skipf("SKIPPING: %v (set FOLIO_PANDOC)", err)
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		t.Skipf("SKIPPING: %v (set FOLIO_TYPST)", err)
	}
	cfg, err := config.Load(os.LookupEnv, config.ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := bundled.Materialize(filepath.Join(os.TempDir(), "folio-test-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	limits := sandbox.DefaultLimits()
	limits.Timeout, limits.MemoryBytes = cfg.RenderTimeout, cfg.RenderMemory
	engine, err := render.NewEngine(render.EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const id = "0192abcd-0000-7000-8000-000000000001"
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="2300" height="2300"><rect width="2300" height="2300" fill="#1F4FD8"/></svg>`
	if err := os.WriteFile(filepath.Join(dir, id+".svg"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &dirAssets{dir: dir, engine: engine, max: cfg.MaxAssetBytes, cache: map[string]model.ResolvedAsset{}}
	_, err = d.Asset(context.Background(), id, false)
	if !errors.Is(err, assets.ErrTooLarge) {
		t.Fatalf("err = %v, want %v", err, assets.ErrTooLarge)
	}
}
