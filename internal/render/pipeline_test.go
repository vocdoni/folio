// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/validate"
)

type memAssets struct {
	mu sync.Mutex
	m  map[string]model.ResolvedAsset
}

func (m *memAssets) Asset(_ context.Context, id string, _ bool) (model.ResolvedAsset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.m[id]
	if !ok {
		return model.ResolvedAsset{}, model.ErrAssetNotFound
	}
	return a, nil
}

const logoID = "0192abcd-0000-7000-8000-000000000001"

func testPipeline(t *testing.T) (*Pipeline, model.Template, model.Brand) {
	t.Helper()
	e := testEngine(t)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="60"><rect width="200" height="60" rx="8" fill="#1F4FD8"/></svg>`)
	png, err := e.SVGToPNG(context.Background(), svg)
	if err != nil {
		t.Fatal(err)
	}
	assets := &memAssets{m: map[string]model.ResolvedAsset{logoID: {ID: logoID, MIME: "image/svg+xml", Data: svg, PNG: png, Width: 625, Height: 188}}}
	p := NewPipeline(PipelineConfig{Engine: e, Queue: NewQueue(2, 32), Assets: assets,
		Limits: validate.Limits{MaxMarkdownBytes: 1 << 20, MaxNesting: 32, MaxTableCells: 10000}, MaxPages: 200})
	tpl := model.Template{
		ID: "proposal", Name: "Proposal", Category: "proposal", BrandID: "example", Languages: []string{"en", "es"},
		Layout:     model.Layout{Base: "proposal", Options: json.RawMessage(`{"cover":true,"toc":true,"page_size":"a4","heading_numbering":true}`)},
		Components: []string{"budget", "info"},
		Sections:   []model.Section{{Title: "Executive summary", Required: true}, {Title: "Budget", Required: true, Component: "budget"}},
	}
	brand := model.Brand{ID: "example", Name: "Example Co",
		Colors: model.BrandColors{Primary: "#1F4FD8", Secondary: "#0B1F44", Accent: "#F2A900", Text: "#1A1A1A", Muted: "#6B7280", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Source Serif 4", Mono: "JetBrains Mono"},
		Logo:   &model.BrandLogo{AssetID: logoID, WidthMM: 35},
		Header: model.BrandHeader{Text: "Example Co", ShowLogo: true},
		Footer: model.BrandFooter{Text: "Example Co · example.com", Legal: "Example Co S.L.", PageNumbers: true}}
	return p, tpl, brand
}

func input() model.DocumentInput {
	return model.DocumentInput{
		Markdown: "# Executive summary\n\nWe propose *things* with $E = m c^2$.\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
		Metadata: json.RawMessage(`{"title":"Website redesign","date":"2026-10-18","language":"es","client":"ACME"}`),
		Data: json.RawMessage(`{"budget":{"items":[{"description":"Design","quantity":"2","unit_price":"1.500,00 €","amount":"3.000,00 €"}],` +
			`"totals":[{"label":"IVA (21 %)","amount":"630,00 €"},{"label":"Total","amount":"3.630,00 €","emphasis":true}]}}`),
	}
}

func TestPipelineDeterministicRender(t *testing.T) {
	p, tpl, brand := testPipeline(t)
	req := Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: input()}
	r1, err := p.Render(context.Background(), req)
	if err != nil {
		var me *model.Error
		if errors.As(err, &me) {
			t.Fatalf("%v %+v", err, me.Details)
		}
		t.Fatal(err)
	}
	r2, err := p.Render(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(r1.PDF) != sha256.Sum256(r2.PDF) || sha256.Sum256(r1.DOCX) != sha256.Sum256(r2.DOCX) {
		t.Fatal("renders are not byte-identical")
	}
	if r1.PageCount < 3 {
		t.Fatalf("pages=%d warnings=%+v", r1.PageCount, r1.Warnings)
	}
	for _, w := range r1.Warnings {
		t.Logf("warning: %+v", w)
	}
}

func TestPipelineValidationFailedDetails(t *testing.T) {
	p, tpl, brand := testPipeline(t)
	in := input()
	in.Markdown = "# Intro\n\n![x](https://evil.example/x.png)\n\n::: folio-milestones\n:::\n"
	_, err := p.Render(context.Background(), Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: in, ValidateOnly: true})
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrValidationFailed {
		t.Fatalf("err = %v", err)
	}
	want := map[string]bool{model.CodeMissingSection: false, model.CodeInvalidImage: false, model.CodeComponentNotAllowed: false}
	for _, f := range me.Details {
		if _, ok := want[f.Code]; ok {
			want[f.Code] = true
		}
	}
	for c, ok := range want {
		if !ok {
			t.Errorf("missing %s in %+v", c, me.Details)
		}
	}
}

// TestPipelineConcurrencyAndBusy (AC9): 20 concurrent renders complete with
// concurrency 2; with a 3-slot wait queue the excess fails fast with busy.
func TestPipelineConcurrencyAndBusy(t *testing.T) {
	p, tpl, brand := testPipeline(t)
	run := func(n int) (ok, busy int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := p.Render(context.Background(), Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: input()})
				mu.Lock()
				defer mu.Unlock()
				var me *model.Error
				switch {
				case err == nil:
					ok++
				case errors.As(err, &me) && me.Code == model.ErrBusy && me.Retryable:
					busy++
				default:
					t.Errorf("render: %v", err)
				}
			}()
		}
		wg.Wait()
		return ok, busy
	}
	if ok, busy := run(20); ok != 20 || busy != 0 {
		t.Fatalf("queue 32: ok=%d busy=%d", ok, busy)
	}
	p.cfg.Queue = NewQueue(2, 3)
	if ok, busy := run(20); busy == 0 || ok+busy != 20 {
		t.Fatalf("queue 3: ok=%d busy=%d", ok, busy)
	}
}
