// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/checks"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
	"github.com/vocdoni/folio/internal/validate"
)

// Request is one validation or render.
type Request struct {
	Template        model.Template
	TemplateVersion int
	Brand           model.Brand
	BrandVersion    int
	Input           model.DocumentInput
	ValidateOnly    bool
}

// Result of a successful validation or render.
type Result struct {
	Canonical   []byte // canonical input JSON (stored as the revision's input blob)
	InputSHA256 string
	Computed    json.RawMessage
	Warnings    []model.Finding
	PDF         []byte // nil when pdf was not requested or ValidateOnly
	DOCX        []byte
	Pages       [][]byte // every page as PNG at PreviewPPI
	PageCount   int
	Duration    time.Duration
}

// PipelineConfig wires the pipeline.
type PipelineConfig struct {
	Engine   *Engine
	Queue    *Queue
	Assets   model.AssetResolver
	Limits   validate.Limits
	MaxPages int
}

// Pipeline is validate → compute → expand → pandoc → typst → checks.
type Pipeline struct {
	cfg      PipelineConfig
	refMu    sync.Mutex
	refCache map[string][]byte // reference.docx by brand/logo/page hash
}

// NewPipeline returns a pipeline.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	return &Pipeline{cfg: cfg, refCache: map[string][]byte{}}
}

// Engine exposes the tool layer (SVG rasterisation, on-demand previews).
func (p *Pipeline) Engine() *Engine { return p.cfg.Engine }

// DefaultFormats is used when the request lists none.
var DefaultFormats = []model.Format{model.FormatPDF, model.FormatDOCX}

// CanonicalInput is the canonical JSON of everything that determines the
// output, including the resolved template and brand versions, and its SHA-256.
func CanonicalInput(t, b model.Ref, in model.DocumentInput) ([]byte, string, error) {
	formats := append([]model.Format(nil), in.Formats...)
	if len(formats) == 0 {
		formats = append(formats, DefaultFormats...)
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	meta, data := in.Metadata, in.Data
	if len(bytes.TrimSpace(meta)) == 0 {
		meta = json.RawMessage(`{}`)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = json.RawMessage(`{}`)
	}
	raw, err := model.Canonical(map[string]any{
		"template": t, "brand": b, "markdown": in.Markdown,
		"metadata": meta, "data": data, "formats": formats,
	})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func invalid(msg string, args ...any) error {
	return &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf(msg, args...)}
}

func failed(fs []model.Finding) error {
	n := 0
	for _, f := range fs {
		if f.Severity == model.SeverityError {
			n++
		}
	}
	return &model.Error{Code: model.ErrValidationFailed, Details: fs,
		Message: fmt.Sprintf("%d problem(s) must be fixed before rendering; see details (path and hint per finding)", n)}
}

// LayoutOptions decodes layout.options with defaults (validated at template creation).
func LayoutOptions(raw json.RawMessage) (model.LayoutOptions, error) {
	o := model.LayoutOptions{Cover: true, TOC: false, PageSize: "a4", HeadingNumbering: false}
	if len(bytes.TrimSpace(raw)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return o, invalid("layout.options: %v", err)
	}
	if o.PageSize != "a4" && o.PageSize != "letter" {
		return o, invalid("layout.options.page_size must be a4 or letter")
	}
	return o, nil
}

// Render validates and, unless ValidateOnly, renders.
func (p *Pipeline) Render(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()
	t, in := req.Template, req.Input
	formats := in.Formats
	if len(formats) == 0 {
		formats = DefaultFormats
	}
	want := map[model.Format]bool{}
	for _, f := range formats {
		if (f != model.FormatPDF && f != model.FormatDOCX) || want[f] {
			return nil, invalid("formats must be a subset of [pdf, docx] without duplicates")
		}
		want[f] = true
	}
	canonical, sum, err := CanonicalInput(model.Ref{ID: t.ID, Version: req.TemplateVersion}, model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, in)
	if err != nil {
		return nil, invalid("metadata and data must be valid JSON: %v", err)
	}
	metaAny, err := model.DecodeJSON(in.Metadata)
	if err != nil {
		return nil, invalid("metadata: %v", err)
	}
	meta, ok := metaAny.(map[string]any)
	if !ok {
		return nil, invalid("metadata must be a JSON object")
	}
	dataAny, err := model.DecodeJSON(in.Data)
	if err != nil {
		return nil, invalid("data: %v", err)
	}
	data, ok := dataAny.(map[string]any)
	if !ok {
		return nil, invalid("data must be a JSON object")
	}
	opts, err := LayoutOptions(t.Layout.Options)
	if err != nil {
		return nil, err
	}

	// Cheap checks first: nothing reaches pandoc unless these pass.
	if fs := validate.PreScan(in.Markdown, p.cfg.Limits); model.HasErrors(fs) {
		return nil, failed(fs)
	}
	schemas, fs := validate.CompileSchemas(t)
	if model.HasErrors(fs) {
		return nil, &model.Error{Code: model.ErrInternal, Message: "template schemas do not compile", Details: fs}
	}
	if fs := schemas.ValidateMetadata(metaAny); model.HasErrors(fs) {
		return nil, failed(fs)
	}
	lang, _ := meta["language"].(string)
	date, _ := meta["date"].(string)
	day, _ := time.Parse("2006-01-02", date) // validated above
	epoch := day.Unix()
	labels, numberLang := brandkit.ResolveLabels(lang, t.Labels)

	release, err := p.cfg.Queue.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	job, err := p.cfg.Engine.NewJob()
	if err != nil {
		return nil, err
	}
	defer job.Remove() //nolint:errcheck // best effort; swept at startup otherwise

	doc, pwarn, err := p.cfg.Engine.ParseMarkdown(ctx, job, in.Markdown)
	if err != nil {
		return nil, err
	}
	findings := []model.Finding{}
	for _, w := range pwarn {
		findings = append(findings, model.Finding{Code: model.CodePandocWarning, Severity: model.SeverityWarning, Path: "/markdown", Message: w})
	}
	assets := map[string]model.ResolvedAsset{}
	assetOK := func(id string) bool {
		a, err := p.cfg.Assets.Asset(ctx, id, false)
		if err != nil {
			return false
		}
		assets[id] = a
		return true
	}
	ast := validate.CheckAST(doc, t, p.cfg.Limits, assetOK)
	findings = append(findings, ast.Findings...)
	findings = append(findings, schemas.ValidateData(data, ast.Uses)...)
	cctx := components.Context{Lang: numberLang, Labels: labels, Metadata: meta}
	computed, cf := components.Compute(data, ast.Uses, cctx)
	findings = append(findings, cf...)
	if t.Layout.Typst != nil {
		allow, err := bundled.VendoredPackages(p.cfg.Engine.PackagesDir())
		if err != nil {
			return nil, err
		}
		findings = append(findings, ScanTypstImports(*t.Layout.Typst, allow)...)
	}
	if model.HasErrors(findings) {
		return nil, failed(findings)
	}
	computedJSON, err := model.Canonical(computed)
	if err != nil {
		return nil, err
	}
	res := &Result{Canonical: canonical, InputSHA256: sum, Computed: computedJSON}
	if req.ValidateOnly {
		res.Warnings, res.Duration = findings, time.Since(start)
		return res, nil
	}

	// Expand components (top-level blocks only) into inert AST blocks.
	byIndex := map[int]components.Use{}
	for _, u := range ast.Uses {
		byIndex[u.Index] = u
	}
	var blocks []pandocast.Block
	for i, b := range doc.Blocks {
		u, ok := byIndex[i]
		if !ok {
			blocks = append(blocks, b)
			continue
		}
		exp, err := components.Expand(u, data, computed, cctx)
		if err != nil {
			return nil, &model.Error{Code: model.ErrInternal, Message: "component expansion failed", Err: err}
		}
		blocks = append(blocks, exp...)
	}
	doc.Blocks = blocks

	// Asset files: Typst gets the sanitised original, DOCX the PNG rendition.
	for id, a := range assets {
		if err := job.WriteFile("assets/"+id+"."+typstExt(a), a.Data); err != nil {
			return nil, err
		}
		if a.MIME == "image/svg+xml" {
			if err := job.WriteFile("assets/"+id+".png", a.PNG); err != nil {
				return nil, err
			}
		}
	}
	var logo *model.ResolvedAsset
	logoPath := ""
	if req.Brand.Logo != nil {
		a, err := p.cfg.Assets.Asset(ctx, req.Brand.Logo.AssetID, true)
		if err != nil {
			return nil, &model.Error{Code: model.ErrRenderFailed, Message: "the brand logo asset is missing", Err: err}
		}
		logo = &a
		logoPath = "assets/logo." + typstExt(a)
		if err := job.WriteFile(logoPath, a.Data); err != nil {
			return nil, err
		}
	}

	if want[model.FormatDOCX] {
		docxDoc, err := cloneDoc(doc)
		if err != nil {
			return nil, err
		}
		rewriteImages(docxDoc, assets, true)
		if opts.Cover {
			docxDoc.Blocks = append(docxCover(meta, labels), docxDoc.Blocks...)
		}
		ref, err := p.referenceDOCX(req.Brand, logo, opts.PageSize, labels["page"])
		if err != nil {
			return nil, err
		}
		res.DOCX, err = p.cfg.Engine.WriteDOCX(ctx, job, docxDoc, DOCXOptions{
			ReferenceDOCX: ref, TOC: opts.TOC, TOCTitle: labels["toc.title"], NumberSections: opts.HeadingNumbering, Epoch: epoch})
		if err != nil {
			return nil, err
		}
		if fs := checks.DOCX(res.DOCX); len(fs) > 0 {
			return nil, &model.Error{Code: model.ErrRenderFailed, Message: "the DOCX output is invalid", Details: fs}
		}
	}

	typstDoc := doc
	rewriteImages(typstDoc, assets, false)
	if err := p.cfg.Engine.WriteTypstBody(ctx, job, typstDoc); err != nil {
		return nil, err
	}
	layout := `#import "/base/` + t.Layout.Base + `.typ": folio-layout` + "\n"
	if t.Layout.Typst != nil {
		layout = *t.Layout.Typst
	}
	brandJSON, err := brandkit.BrandJSON(req.Brand, logoPath)
	if err != nil {
		return nil, err
	}
	metaJSON, _ := model.Canonical(meta)
	labelsJSON, _ := model.Canonical(labels)
	optsJSON, _ := model.Canonical(opts)
	if err := p.cfg.Engine.WriteTypstInputs(job, TypstInputs{LayoutSource: layout, Meta: metaJSON, Brand: brandJSON, Labels: labelsJSON, Options: optsJSON}); err != nil {
		return nil, err
	}
	pdf, pages, twarn, err := p.cfg.Engine.CompileTypst(ctx, job, epoch)
	if err != nil {
		return nil, err
	}
	n, pf := checks.PDF(ctx, pdf, p.cfg.MaxPages)
	if model.HasErrors(pf) {
		return nil, &model.Error{Code: model.ErrRenderFailed, Message: "the PDF output failed checks", Details: pf}
	}
	po := checks.DefaultPageOptions()
	po.PPI = PreviewPPI
	po.Background = hexColor(req.Brand.Colors.Background)
	pageFindings, err := checks.Pages(pages, po)
	if err != nil {
		return nil, &model.Error{Code: model.ErrInternal, Message: "page checks failed", Err: err}
	}
	findings = append(findings, checks.TypstWarnings(twarn)...)
	findings = append(findings, pageFindings...)
	if want[model.FormatPDF] {
		res.PDF = pdf
	}
	res.Pages, res.PageCount, res.Warnings, res.Duration = pages, n, findings, time.Since(start)
	return res, nil
}

func typstExt(a model.ResolvedAsset) string {
	switch a.MIME {
	case "image/svg+xml":
		return "svg"
	case "image/jpeg":
		return "jpg"
	}
	return "png"
}

// rewriteImages points asset:<id> targets at the files written into the job.
func rewriteImages(doc *pandocast.Document, assets map[string]model.ResolvedAsset, forDOCX bool) {
	pandocast.Walk(doc.Blocks, pandocast.Visitor{Inline: func(in pandocast.Inline, _ int) bool {
		im, ok := in.(*pandocast.Image)
		if !ok {
			return true
		}
		m := validate.AssetTargetRE.FindStringSubmatch(im.Target.URL)
		if m == nil {
			return true // unreachable: CheckAST rejected it
		}
		a := assets[m[1]]
		ext := typstExt(a)
		if forDOCX && ext == "svg" {
			ext = "png"
		}
		im.Target.URL = "assets/" + m[1] + "." + ext
		return true
	}})
}

func cloneDoc(doc *pandocast.Document) (*pandocast.Document, error) {
	raw, err := pandocast.Encode(doc)
	if err != nil {
		return nil, err
	}
	return pandocast.Decode(raw)
}

// docxCover builds the DOCX cover: title, version label, client, project,
// reference, author, date (the PDF layout's order), then a page break.
func docxCover(meta map[string]any, labels map[string]string) []pandocast.Block {
	styled := func(style, text string) pandocast.Block {
		return &pandocast.Div{Attr: pandocast.Attr{ID: "", Classes: []string{}, KeyVals: [][2]string{{"custom-style", style}}},
			Blocks: []pandocast.Block{&pandocast.Para{Inlines: pandocast.Text(text)}}}
	}
	s := func(k string) string { v, _ := meta[k].(string); return v }
	out := []pandocast.Block{styled("Title", s("title"))}
	if v := s("version_label"); v != "" {
		out = append(out, styled("Subtitle", v))
	}
	if v := s("client"); v != "" {
		out = append(out, styled("Subtitle", labels["cover.prepared_for"]+" "+v))
	}
	for _, k := range []string{"project", "reference"} {
		if v := s(k); v != "" {
			out = append(out, styled("Subtitle", v))
		}
	}
	if v := s("author"); v != "" {
		out = append(out, styled("Author", v))
	}
	out = append(out, styled("Date", s("date")))
	// Folio-generated raw OpenXML page break (user raw content is rejected by validation).
	out = append(out, &pandocast.RawBlock{Format: "openxml", Text: `<w:p><w:r><w:br w:type="page"/></w:r></w:p>`})
	return out
}

func (p *Pipeline) referenceDOCX(b model.Brand, logo *model.ResolvedAsset, pageSize, pageLabel string) ([]byte, error) {
	var bl *brandkit.Logo
	h := sha256.New()
	brandJSON, _ := model.Canonical(b)
	h.Write(brandJSON)
	h.Write([]byte("\x00" + pageSize + "\x00" + pageLabel))
	if logo != nil {
		png := logo.PNG
		if png == nil && logo.MIME == "image/png" {
			png = logo.Data
		}
		if png == nil {
			return nil, &model.Error{Code: model.ErrInternal, Message: "brand logo must be PNG or SVG for the DOCX header"}
		}
		bl = &brandkit.Logo{PNG: png, WidthPx: logo.Width, HeightPx: logo.Height, WidthMM: b.Logo.WidthMM}
		sum := sha256.Sum256(png)
		h.Write(sum[:])
	}
	key := hex.EncodeToString(h.Sum(nil))
	p.refMu.Lock()
	defer p.refMu.Unlock()
	if ref, ok := p.refCache[key]; ok {
		return ref, nil
	}
	base, err := bundled.ReferenceDOCX()
	if err != nil {
		return nil, err
	}
	ref, err := brandkit.ReferenceDOCX(base, b, bl, pageSize, pageLabel)
	if err != nil {
		return nil, &model.Error{Code: model.ErrInternal, Message: "could not build the brand reference document", Err: err}
	}
	if len(p.refCache) >= 64 {
		p.refCache = map[string][]byte{}
	}
	p.refCache[key] = ref
	return ref, nil
}

func hexColor(s string) [3]byte {
	if len(s) != 7 || s[0] != '#' {
		return [3]byte{255, 255, 255}
	}
	var out [3]byte
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(s[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return [3]byte{255, 255, 255}
		}
		out[i] = byte(v)
	}
	return out
}
