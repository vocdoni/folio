// SPDX-License-Identifier: AGPL-3.0-only

package render_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"flag"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// starterPipeline builds a pipeline over the pinned tools with the seed logo,
// and returns it with the seed brand and starter templates.
func starterPipeline(t *testing.T) (*render.Pipeline, model.Brand, []model.Template) {
	t.Helper()
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
	svg := seed.Logo()
	png, err := e.SVGToPNG(context.Background(), svg)
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
	return p, brand, templates
}

// TestGoldenStarterTemplates (AC1): each starter sample renders twice to
// identical PDF and DOCX bytes that match testdata/golden.json.
func TestGoldenStarterTemplates(t *testing.T) {
	p, brand, templates := starterPipeline(t)
	ctx := context.Background()
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

// The proposal starter's DOCX opens with the cover (Title paragraph), then the
// Folio-emitted table of contents after the cover's page break, and its
// component tables carry content-proportional column widths instead of
// pandoc's equal default grid.
func TestStarterProposalDOCXFrontMatterAndTableWidths(t *testing.T) {
	p, brand, templates := starterPipeline(t)
	var tpl model.Template
	for _, x := range templates {
		if x.ID == "proposal" {
			tpl = x
		}
	}
	if tpl.ID == "" {
		t.Fatal("no proposal starter template")
	}
	res, err := p.Render(context.Background(), render.Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1,
		Input: model.DocumentInput{Markdown: tpl.Sample.Markdown, Metadata: tpl.Sample.Metadata, Data: tpl.Sample.Data}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(res.DOCX), int64(len(res.DOCX)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" || f.Name == "word/styles.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			parts[f.Name] = string(b)
		}
	}
	body := parts["word/document.xml"]
	if body == "" || parts["word/styles.xml"] == "" {
		t.Fatal("word/document.xml or word/styles.xml missing")
	}
	// The TOC heading uses the reference document's TOC Heading style; it must
	// not add a second style with the same ID.
	if n := strings.Count(parts["word/styles.xml"], `w:styleId="TOCHeading"`); n != 1 {
		t.Fatalf("%d TOCHeading style definitions, want 1", n)
	}
	if !regexp.MustCompile(`<w:pStyle w:val="TOCHeading"\s*/>`).MatchString(body) {
		t.Fatal("no TOC heading paragraph")
	}
	index := func(expr string) int {
		if loc := regexp.MustCompile(expr).FindStringIndex(body); loc != nil {
			return loc[0]
		}
		return -1
	}
	title := index(`<w:pStyle w:val="Title"\s*/>`)
	pageBreak := index(`<w:br w:type="page"\s*/>`)
	toc := index(`<w:docPartGallery w:val="Table of Contents"\s*/>`) // pandoc's own --toc uses the same marker
	if title < 0 || pageBreak < 0 || toc < 0 {
		t.Fatalf("title=%d page break=%d toc=%d", title, pageBreak, toc)
	}
	if title > pageBreak || pageBreak > toc {
		t.Fatalf("DOCX body order: title at %d, page break at %d, TOC at %d; want cover, break, TOC", title, pageBreak, toc)
	}
	if n := strings.Count(body, "Table of Contents"); n != 1 {
		t.Fatalf("%d tables of contents, want 1", n)
	}
	// The TOC is followed directly by a page break, so the body starts on a new page.
	if !regexp.MustCompile(`</w:sdt>\s*<w:p><w:r><w:br w:type="page"\s*/></w:r></w:p>`).MatchString(body[toc:]) {
		t.Fatal("no page break directly after the table of contents")
	}
	// Every table's grid: at least one multi-column table must have unequal widths,
	// and no table with more than one column may use an equal default grid.
	gridRE := regexp.MustCompile(`<w:tblGrid>(.*?)</w:tblGrid>`)
	colRE := regexp.MustCompile(`<w:gridCol w:w="([0-9]+)"`)
	grids := gridRE.FindAllStringSubmatch(body, -1)
	if len(grids) == 0 {
		t.Fatal("no tables in the proposal DOCX")
	}
	unequal := 0
	for _, g := range grids {
		cols := colRE.FindAllStringSubmatch(g[1], -1)
		if len(cols) < 2 {
			continue
		}
		equal := true
		for _, c := range cols[1:] {
			equal = equal && c[1] == cols[0][1]
		}
		if equal {
			t.Errorf("table grid with equal column widths: %s", g[1])
		} else {
			unequal++
		}
	}
	if unequal == 0 {
		t.Fatal("no table with content-proportional column widths")
	}
	checkShortColumnsFit(t, body)
}

// DOCX table structure, enough to measure column text (namespaces are matched
// by local name).
type docxTable struct {
	Grid []struct {
		W int `xml:"w,attr"`
	} `xml:"tblGrid>gridCol"`
	Rows []struct {
		Cells []struct {
			Span *struct {
				Val int `xml:"val,attr"`
			} `xml:"tcPr>gridSpan"`
			Paras []struct {
				Runs []struct {
					Bold *struct{} `xml:"rPr>b"`
					Text []string  `xml:"t"`
				} `xml:"r"`
			} `xml:"p"`
		} `xml:"tc"`
	} `xml:"tr"`
}

// checkShortColumnsFit asserts that every short column (longest text of at
// most 16 runes: numbers, money, dates, units) is at least as wide as its
// need: longest text (bold ×1.1, rounded up) plus 3 runes of padding, at 80
// runes per full A4 text width (the sum of the grid). Such cells never wrap.
func checkShortColumnsFit(t *testing.T, body string) {
	t.Helper()
	const avail, short, padding = 80.0, 16, 3
	dec := xml.NewDecoder(strings.NewReader(body))
	tables := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "tbl" {
			continue
		}
		var tbl docxTable
		if err := dec.DecodeElement(&tbl, &se); err != nil {
			t.Fatal(err)
		}
		tables++
		gridSum := 0
		for _, g := range tbl.Grid {
			gridSum += g.W
		}
		longest := make([]int, len(tbl.Grid))
		texts := make([]string, len(tbl.Grid))
		for _, r := range tbl.Rows {
			col := 0
			for _, c := range r.Cells {
				span := 1
				if c.Span != nil && c.Span.Val > 1 {
					span = c.Span.Val
				}
				if span == 1 && col < len(longest) {
					var n float64
					var text strings.Builder
					for pi, p := range c.Paras {
						if pi > 0 {
							text.WriteString(" ")
						}
						for _, run := range p.Runs {
							s := strings.Join(run.Text, "")
							text.WriteString(s)
							w := 1.0
							if run.Bold != nil {
								w = 1.1
							}
							n += w * float64(len([]rune(s)))
						}
					}
					if l := int(math.Ceil(n - 1e-9)); l > longest[col] {
						longest[col], texts[col] = l, text.String()
					}
				}
				col += span
			}
		}
		runeTwips := float64(gridSum) / avail
		for i, l := range longest {
			if l == 0 || l > short {
				continue
			}
			need := float64(l+padding) * runeTwips
			if float64(tbl.Grid[i].W) < need-2 {
				t.Errorf("table %d column %d (%q): %d twips < need %.0f twips (%d runes + %d padding)", tables, i, texts[i], tbl.Grid[i].W, need, l, padding)
			}
		}
	}
	if tables == 0 {
		t.Fatal("no tables measured")
	}
}
