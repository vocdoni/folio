// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
	"github.com/vocdoni/folio/internal/sandbox"
)

// Dialect is the only pandoc reader configuration Folio uses for user Markdown.
const Dialect = "markdown-raw_html-raw_tex-raw_attribute-yaml_metadata_block-citations-implicit_header_references"

// PreviewPPI is the resolution of PNG page previews.
const PreviewPPI = 144

// MainTyp is the fixed Typst entry point written into every job.
const MainTyp = `#import "layout.typ": folio-layout
#show: folio-layout.with(
  meta: json("meta.json"), brand: json("brand.json"),
  labels: json("labels.json"), options: json("options.json"),
)
#include "body.typ"
`

// EngineConfig configures the external-tool layer.
type EngineConfig struct {
	Runtime     bundled.Runtime
	PackagesDir string // overrides Runtime.PackagesDir (tests use a fixture)
	Pandoc      string // absolute paths (sandbox.LookupTool)
	Typst       string
	Self        string // binary providing `sandbox-exec`; os.Executable() when empty
	JobRoot     string // parent of job dirs; os.TempDir() when empty
	Limits      sandbox.Limits
	ExtraEnv    []string // tests only (e.g. a proxy pointing at a probe listener)
	ExtraRO     []string // tests only (shared libraries of a -race test binary)
}

// Engine runs pandoc and typst inside per-job directories through the sandbox.
type Engine struct {
	cfg    EngineConfig
	runner *sandbox.Runner
}

// NewEngine validates cfg.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Pandoc == "" || cfg.Typst == "" || cfg.Runtime.Root == "" {
		return nil, errors.New("render: engine needs pandoc, typst and a runtime dir")
	}
	if cfg.PackagesDir == "" {
		cfg.PackagesDir = cfg.Runtime.PackagesDir
	}
	if cfg.JobRoot == "" {
		cfg.JobRoot = os.TempDir()
	}
	return &Engine{cfg: cfg, runner: &sandbox.Runner{Self: cfg.Self}}, nil
}

// NewJob creates a job directory under the configured root.
func (e *Engine) NewJob() (*Job, error) { return NewJob(e.cfg.JobRoot) }

// PackagesDir is the vendored package directory passed to typst.
func (e *Engine) PackagesDir() string { return e.cfg.PackagesDir }

func (e *Engine) run(ctx context.Context, job *Job, tool string, env []string, args ...string) (sandbox.Result, error) {
	spec := sandbox.Spec{
		Tool:     tool,
		Args:     args,
		JobDir:   job.Dir,
		ReadOnly: append([]string{e.cfg.Runtime.Root, e.cfg.PackagesDir}, e.cfg.ExtraRO...),
		Env:      append(env, e.cfg.ExtraEnv...),
		Limits:   e.cfg.Limits,
	}
	res, err := e.runner.Run(ctx, spec, nil)
	name := filepath.Base(tool)
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, sandbox.ErrTimeout):
		return res, &model.Error{Code: model.ErrTimeout, Err: err,
			Message: fmt.Sprintf("%s exceeded the %s render time limit; simplify the document (fewer nested blocks, smaller tables); retrying unchanged will time out again", name, e.cfg.Limits.Timeout)}
	default:
		var ee *sandbox.ExitError
		if errors.As(err, &ee) && ee.Code != 125 && ee.Code != 126 {
			return res, &model.Error{Code: model.ErrRenderFailed, Err: err,
				Message: fmt.Sprintf("%s failed: %s", name, diagnostics(job, ee.Stderr))}
		}
		return res, &model.Error{Code: model.ErrInternal, Err: err, Message: name + " could not be started"}
	}
}

// diagnostics returns at most 20 lines of tool stderr with the job path removed.
func diagnostics(job *Job, stderr []byte) string {
	s := strings.ReplaceAll(string(stderr), job.Dir+string(filepath.Separator), "")
	s = strings.ReplaceAll(s, job.Dir, ".")
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], "…")
	}
	return strings.Join(lines, "\n")
}

// ParseMarkdown converts user Markdown to the pandoc AST with the fixed
// dialect under --sandbox. Pandoc warnings are returned as text lines; a
// "Could not fetch resource" warning is an error.
func (e *Engine) ParseMarkdown(ctx context.Context, job *Job, markdown string) (*pandocast.Document, []string, error) {
	if err := job.WriteFile("input.md", []byte(markdown)); err != nil {
		return nil, nil, err
	}
	res, err := e.run(ctx, job, e.cfg.Pandoc, nil,
		"--sandbox", "-f", Dialect, "-t", "json", "-o", "ast.json", "input.md")
	if err != nil {
		return nil, nil, err
	}
	warnings, err := pandocWarnings(res.Stderr)
	if err != nil {
		return nil, warnings, err
	}
	raw, err := job.ReadFile("ast.json")
	if err != nil {
		return nil, warnings, err
	}
	doc, err := pandocast.Decode(raw)
	if err != nil {
		return nil, warnings, &model.Error{Code: model.ErrRenderFailed, Message: "pandoc produced an unexpected document", Err: err}
	}
	return doc, warnings, nil
}

func pandocWarnings(stderr []byte) ([]string, error) {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(stderr)), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.Contains(l, "Could not fetch resource") {
			return out, &model.Error{Code: model.ErrRenderFailed, Message: "pandoc could not fetch a resource: " + l}
		}
		out = append(out, l)
	}
	return out, nil
}

// DOCXOptions maps layout options to pandoc flags.
type DOCXOptions struct {
	ReferenceDOCX  []byte
	NumberSections bool
	Epoch          int64 // SOURCE_DATE_EPOCH
}

// WriteDOCX converts a Folio-built AST (images already rewritten to
// "assets/<id>.<ext>" and written into the job) to DOCX. Folio's own Lua
// filter is the only filter ever passed.
func (e *Engine) WriteDOCX(ctx context.Context, job *Job, doc *pandocast.Document, opt DOCXOptions) ([]byte, error) {
	raw, err := pandocast.Encode(doc)
	if err != nil {
		return nil, err
	}
	if err := job.WriteFile("docx.json", raw); err != nil {
		return nil, err
	}
	if err := job.WriteFile("reference.docx", opt.ReferenceDOCX); err != nil {
		return nil, err
	}
	args := []string{"--sandbox", "-f", "json", "-t", "docx",
		"--reference-doc=reference.docx", "--lua-filter=" + e.cfg.Runtime.LuaFilter}
	if opt.NumberSections {
		args = append(args, "--number-sections")
	}
	args = append(args, "-o", "out.docx", "docx.json")
	res, err := e.run(ctx, job, e.cfg.Pandoc, []string{"SOURCE_DATE_EPOCH=" + strconv.FormatInt(opt.Epoch, 10)}, args...)
	if err != nil {
		return nil, err
	}
	if _, err := pandocWarnings(res.Stderr); err != nil {
		return nil, err
	}
	return job.ReadFile("out.docx")
}

// WriteTypstBody converts a Folio-built AST to body.typ (no --standalone).
func (e *Engine) WriteTypstBody(ctx context.Context, job *Job, doc *pandocast.Document) error {
	raw, err := pandocast.Encode(doc)
	if err != nil {
		return err
	}
	if err := job.WriteFile("typst.json", raw); err != nil {
		return err
	}
	res, err := e.run(ctx, job, e.cfg.Pandoc, nil, "--sandbox", "-f", "json", "-t", "typst", "-o", "body.typ", "typst.json")
	if err != nil {
		return err
	}
	_, err = pandocWarnings(res.Stderr)
	return err
}

// TypstInputs are the JSON side files and the layout source for main.typ.
type TypstInputs struct {
	LayoutSource string // layout.typ: a base layout re-export or the template's custom source
	Meta         []byte
	Brand        []byte
	Labels       []byte
	Options      []byte
}

// WriteTypstInputs writes main.typ, layout.typ, the JSON side files and a
// copy of every base layout under base/ (so custom layouts can
// `#import "/base/<name>.typ"`).
func (e *Engine) WriteTypstInputs(job *Job, in TypstInputs) error {
	files := map[string][]byte{
		"main.typ":     []byte(MainTyp),
		"layout.typ":   []byte(in.LayoutSource),
		"meta.json":    in.Meta,
		"brand.json":   in.Brand,
		"labels.json":  in.Labels,
		"options.json": in.Options,
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := job.WriteFile(n, files[n]); err != nil {
			return err
		}
	}
	return fs.WalkDir(bundled.FS(), "typst/base", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".typ" {
			return err
		}
		b, err := fs.ReadFile(bundled.FS(), p)
		if err != nil {
			return err
		}
		return job.WriteFile(filepath.Join("base", filepath.Base(p)), b)
	})
}

func (e *Engine) typstArgs(job *Job, epoch int64) []string {
	return []string{"compile",
		"--root", job.Dir,
		"--font-path", e.cfg.Runtime.FontsDir,
		"--ignore-system-fonts", "--ignore-embedded-fonts",
		"--creation-timestamp", strconv.FormatInt(epoch, 10),
		"--package-path", e.cfg.PackagesDir,
		"--package-cache-path", filepath.Join(job.Dir, "pkgcache"),
		"--jobs", "2",
		"--diagnostic-format", "short",
	}
}

func typstWarnings(job *Job, stderr []byte) []string {
	var out []string
	for _, l := range strings.Split(diagnostics(job, stderr), "\n") {
		if strings.Contains(l, ": warning: ") || strings.HasPrefix(l, "warning: ") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// CompileTypst compiles main.typ to out.pdf and to PNG pages at PreviewPPI.
// It returns the PDF, the PNG pages in order and typst warnings. With maxPages
// > 0 the PNG export stops after maxPages+1 pages, so an oversized document is
// not rasterised in full before the page limit refuses it.
func (e *Engine) CompileTypst(ctx context.Context, job *Job, epoch int64, maxPages int) ([]byte, [][]byte, []string, error) {
	if err := job.MkdirAll("pkgcache"); err != nil {
		return nil, nil, nil, err
	}
	if err := job.MkdirAll("pages"); err != nil {
		return nil, nil, nil, err
	}
	res, err := e.run(ctx, job, e.cfg.Typst, nil, append(e.typstArgs(job, epoch), "main.typ", "out.pdf")...)
	if err != nil {
		return nil, nil, nil, err
	}
	warnings := typstWarnings(job, res.Stderr)
	pdf, err := job.ReadFile("out.pdf")
	if err != nil {
		return nil, nil, warnings, err
	}
	args := append(e.typstArgs(job, epoch), "--ppi", strconv.Itoa(PreviewPPI))
	if maxPages > 0 {
		args = append(args, "--pages", "1-"+strconv.Itoa(maxPages+1)) // one extra so the PDF check still reports the overflow
	}
	args = append(args, "main.typ", "pages/page-{0p}.png")
	if _, err := e.run(ctx, job, e.cfg.Typst, nil, args...); err != nil {
		return nil, nil, warnings, err
	}
	pages, err := readPages(job)
	return pdf, pages, warnings, err
}

func readPages(job *Job) ([][]byte, error) {
	dir, _ := job.Path("pages")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("render: pages: %w", err)
	}
	type pg struct {
		n    int
		name string
	}
	var ps []pg
	for _, en := range entries {
		num := strings.TrimSuffix(strings.TrimPrefix(en.Name(), "page-"), ".png")
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		ps = append(ps, pg{n, en.Name()})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].n < ps[j].n })
	out := make([][]byte, 0, len(ps))
	for _, p := range ps {
		b, err := job.ReadFile(filepath.Join("pages", p.name))
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// SVGToPNG rasterises a sanitised SVG at 300 ppi with typst (PNG rendition
// for the DOCX path).
func (e *Engine) SVGToPNG(ctx context.Context, svg []byte) ([]byte, error) {
	return e.rasterize(ctx, "image.svg", svg, `#image("image.svg")`, 300)
}

// RasterizePDFPage renders one page (1-based) of a stored PDF to PNG.
func (e *Engine) RasterizePDFPage(ctx context.Context, pdf []byte, page, ppi int) ([]byte, error) {
	return e.rasterize(ctx, "doc.pdf", pdf, fmt.Sprintf(`#image("doc.pdf", page: %d)`, page), ppi)
}

func (e *Engine) rasterize(ctx context.Context, name string, data []byte, body string, ppi int) ([]byte, error) {
	job, err := e.NewJob()
	if err != nil {
		return nil, err
	}
	defer job.Remove() //nolint:errcheck // best effort
	src := "#set page(width: auto, height: auto, margin: 0pt)\n" + body + "\n"
	if err := job.WriteFile(name, data); err != nil {
		return nil, err
	}
	if err := job.WriteFile("raster.typ", []byte(src)); err != nil {
		return nil, err
	}
	if err := job.MkdirAll("pkgcache"); err != nil {
		return nil, err
	}
	args := append(e.typstArgs(job, 0), "--ppi", strconv.Itoa(ppi), "raster.typ", "out.png")
	if _, err := e.run(ctx, job, e.cfg.Typst, nil, args...); err != nil {
		return nil, err
	}
	out, err := job.ReadFile("out.png")
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG")) {
		return nil, &model.Error{Code: model.ErrRenderFailed, Message: "typst did not produce a PNG"}
	}
	return out, nil
}
