// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/sandbox"
	"github.com/vocdoni/folio/internal/validate"
)

// Request is the `folio render` input file.
type Request struct {
	Template        model.Template `json:"template"`
	TemplateVersion int            `json:"template_version,omitempty"` // default 1
	Brand           model.Brand    `json:"brand"`
	BrandVersion    int            `json:"brand_version,omitempty"` // default 1
	AssetsDir       string         `json:"assets_dir,omitempty"`    // files named <asset-id>.<png|jpg|svg>, relative to the request file
	Document        struct {
		model.DocumentInput
		MarkdownFile string `json:"markdown_file,omitempty"` // alternative to markdown, relative to the request file
	} `json:"document"`
}

// dirAssets resolves assets from a directory, sanitising each file exactly as
// uploads are sanitised and rasterising SVGs for the DOCX path.
type dirAssets struct {
	dir    string
	engine *render.Engine
	max    int64
	mu     sync.Mutex
	cache  map[string]model.ResolvedAsset
}

func (d *dirAssets) Asset(ctx context.Context, id string, _ bool) (model.ResolvedAsset, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.cache[id]; ok {
		return a, nil
	}
	if d.dir == "" || strings.ContainsAny(id, `/\.`) {
		return model.ResolvedAsset{}, model.ErrAssetNotFound
	}
	for _, ext := range []string{"png", "jpg", "jpeg", "webp", "svg"} {
		raw, err := os.ReadFile(filepath.Join(d.dir, id+"."+ext))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return model.ResolvedAsset{}, err
		}
		san, err := assets.Sanitize(raw, d.max)
		if err != nil {
			return model.ResolvedAsset{}, fmt.Errorf("asset %s: %w", id, err)
		}
		a := model.ResolvedAsset{ID: id, MIME: san.MIME, Data: san.Data, Width: san.Width, Height: san.Height}
		if san.MIME == assets.MIMESVG {
			if a.PNG, err = d.engine.SVGToPNG(ctx, san.Data); err != nil {
				return model.ResolvedAsset{}, fmt.Errorf("asset %s: %w", id, err)
			}
		}
		d.cache[id] = a
		return a, nil
	}
	return model.ResolvedAsset{}, model.ErrAssetNotFound
}

func renderCmd(args []string) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	reqPath := fs.String("request", "", "JSON request file (template, brand, assets_dir, document)")
	outDir := fs.String("out", "", "output directory (created if missing)")
	runtimeDir := fs.String("runtime-dir", defaultRuntimeDir(), "where bundled fonts and layouts are materialised")
	if err := fs.Parse(args); err != nil || *reqPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: folio render -request req.json -out DIR [-runtime-dir DIR]")
		return 2
	}
	cfg, err := config.Load(os.LookupEnv, config.ModeRender)
	if err != nil {
		fmt.Fprintln(os.Stderr, "folio:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runRender(ctx, cfg, *reqPath, *outDir, *runtimeDir); err != nil {
		var me *model.Error
		if errors.As(err, &me) {
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			_ = enc.Encode(me)
		} else {
			fmt.Fprintln(os.Stderr, "folio:", err)
		}
		return 1
	}
	return 0
}

func defaultRuntimeDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "folio", "runtime")
	}
	return filepath.Join(os.TempDir(), "folio-runtime")
}

func runRender(ctx context.Context, cfg *config.Config, reqPath, outDir, runtimeDir string) error {
	raw, err := os.ReadFile(reqPath)
	if err != nil {
		return err
	}
	var req Request
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	base := filepath.Dir(reqPath)
	if req.Document.MarkdownFile != "" {
		md, err := os.ReadFile(filepath.Join(base, req.Document.MarkdownFile))
		if err != nil {
			return err
		}
		req.Document.Markdown = string(md)
	}
	if req.TemplateVersion == 0 {
		req.TemplateVersion = 1
	}
	if req.BrandVersion == 0 {
		req.BrandVersion = 1
	}
	rt, err := bundled.Materialize(runtimeDir)
	if err != nil {
		return err
	}
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		return err
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		return err
	}
	limits := sandbox.DefaultLimits()
	limits.Timeout, limits.MemoryBytes = cfg.RenderTimeout, cfg.RenderMemory
	engine, err := render.NewEngine(render.EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Limits: limits})
	if err != nil {
		return err
	}
	assetsDir := ""
	if req.AssetsDir != "" {
		assetsDir = filepath.Join(base, req.AssetsDir)
	}
	p := render.NewPipeline(render.PipelineConfig{
		Engine: engine, Queue: render.NewQueue(1, 0),
		Assets:   &dirAssets{dir: assetsDir, engine: engine, max: cfg.MaxAssetBytes, cache: map[string]model.ResolvedAsset{}},
		Limits:   validate.Limits{MaxMarkdownBytes: cfg.MaxMarkdownBytes, MaxNesting: cfg.MaxNesting, MaxTableCells: cfg.MaxTableCells},
		MaxPages: cfg.MaxPages,
	})
	res, err := p.Render(ctx, render.Request{Template: req.Template, TemplateVersion: req.TemplateVersion,
		Brand: req.Brand, BrandVersion: req.BrandVersion, Input: req.Document.DocumentInput})
	if err != nil {
		return err
	}
	return writeOutputs(outDir, res)
}

func writeOutputs(dir string, res *render.Result) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		return os.WriteFile(filepath.Join(dir, name), data, 0o640)
	}
	if res.PDF != nil {
		if err := write("document.pdf", res.PDF); err != nil {
			return err
		}
	}
	if res.DOCX != nil {
		if err := write("document.docx", res.DOCX); err != nil {
			return err
		}
	}
	for i, pg := range res.Pages {
		if err := write(fmt.Sprintf("page-%d.png", i+1), pg); err != nil {
			return err
		}
	}
	summary, _ := json.MarshalIndent(map[string]any{
		"input_sha256": res.InputSHA256, "pages": res.PageCount, "computed": res.Computed, "warnings": res.Warnings,
	}, "", "  ")
	return write("result.json", append(summary, '\n'))
}
