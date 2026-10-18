// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/render"
)

// fakeConvert stands in for render.Engine.
type fakeConvert struct{}

func (fakeConvert) DOCXToMarkdown(context.Context, []byte) (string, error) {
	return "# Scope\n\nConverted from DOCX.", nil
}
func (fakeConvert) RasterizePDFPage(_ context.Context, _ []byte, page, _ int) ([]byte, error) {
	return testPNG(80+page, 100), nil
}

func draftJSON(metaSchema string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "memo-ai", "name": "AI memo", "description": "Drafted.", "category": "memo", "brand_id": "acme", "languages": []string{"en"},
		"layout":               map[string]any{"base": "report", "cover": false, "toc": false, "page_size": "a4", "heading_numbering": false},
		"metadata_schema_json": metaSchema, "data_schema_json": "",
		"sections":   []any{map[string]any{"title": "Summary", "required": true, "aliases": []string{}, "hint": "", "component": ""}},
		"components": []string{}, "strict": false, "body_skeleton": "# Summary\n\nTODO\n",
		"sample": map[string]any{"markdown": "# Summary\n\nAll good.", "metadata_json": `{"title":"Memo","date":"2026-10-18","language":"en"}`, "data_json": ""},
	})
	return string(b)
}

func newDraftServices(t *testing.T, stub *aiStub) *svc {
	t.Helper()
	pr := newFakeRenderer(t)
	sv := newAIServices(t, stub, func(o *Options) {
		c := *o.Catalog
		c.Renderer = pr
		o.Preview, o.PreviewQueue, o.Convert = &c, render.NewQueue(1, 4), fakeConvert{}
	})
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	return sv
}

// TestTemplateDraftRepairRound (B-AC5): an invalid schema string, then a
// valid spec: the repair round runs and the job ends done with a preview.
func TestTemplateDraftRepairRound(t *testing.T) {
	stub := &aiStub{answers: map[string][]string{"template_draft": {draftJSON("{not json"), draftJSON(`{"type":"object","properties":{"team":{"type":"string"}}}`)}}}
	sv := newDraftServices(t, stub)
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/template-draft", TemplateDraftIn{Description: "A one-page internal memo."}, nil), http.StatusAccepted)
	j := sv.waitJob(started.JobID)
	raw, _ := json.Marshal(j.Result)
	var d TemplateDraftOut
	_ = json.Unmarshal(raw, &d)
	if j.State != jobDone || !d.OK || !d.Repaired || d.Spec.ID != "memo-ai" || d.Spec.Layout.Typst != nil || len(d.PageURLs) != 2 || stub.count("template_draft") != 2 {
		t.Fatalf("draft %s (%d calls)", raw, stub.count("template_draft"))
	}
	if w := sv.do("GET", d.PageURLs[0], nil, nil); w.Code != 200 {
		t.Fatalf("draft preview page: %d", w.Code)
	}
	var tpls int
	_ = sv.opts.DB.R.QueryRow(`SELECT count(*) FROM templates`).Scan(&tpls)
	if tpls != 0 {
		t.Fatal("a draft must never be saved")
	}
}

func TestTemplateDraftExamples(t *testing.T) {
	stub := &aiStub{answers: map[string][]string{"template_draft": {draftJSON("")}}}
	sv := newDraftServices(t, stub)
	pdf, err := os.ReadFile(filepath.Join("..", "checks", "testdata", "two-pages.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/template-draft",
		TemplateDraftIn{ExampleName: "example.pdf", ExampleBase64: base64.StdEncoding.EncodeToString(pdf)}, nil), http.StatusAccepted)
	if j := sv.waitJob(started.JobID); j.State != jobDone {
		t.Fatalf("pdf example: %+v", j)
	}
	stub.mu.Lock()
	body, _ := json.Marshal(stub.reqs[len(stub.reqs)-1])
	stub.mu.Unlock()
	if n := strings.Count(string(body), "data:image/png;base64,"); n != 2 {
		t.Fatalf("the PDF's 2 pages must be sent as images, got %d", n)
	}
	docx := append([]byte("PK\x03\x04"), []byte("word/document.xml")...)
	started = decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/template-draft",
		TemplateDraftIn{ExampleName: "example.docx", ExampleBase64: base64.StdEncoding.EncodeToString(docx)}, nil), http.StatusAccepted)
	sv.waitJob(started.JobID)
	stub.mu.Lock()
	body, _ = json.Marshal(stub.reqs[len(stub.reqs)-1])
	stub.mu.Unlock()
	if !strings.Contains(string(body), "Converted from DOCX") {
		t.Fatal("the DOCX must be converted to Markdown")
	}
	for name, in := range map[string]TemplateDraftIn{
		"nothing": {},
		"binary":  {ExampleName: "x.bin", ExampleBase64: base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 0xff})},
		"base64":  {ExampleBase64: "***"},
	} {
		if w := sv.do("POST", "/ui/api/ai/template-draft", in, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// A damaged PDF is read inside the job, off the request path: the job
	// ends in error with invalid_input.
	started = decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/template-draft",
		TemplateDraftIn{ExampleName: "x.pdf", ExampleBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 garbage"))}, nil), http.StatusAccepted)
	if j := sv.waitJob(started.JobID); j.State != jobError || j.Error == nil || j.Error.Code != "invalid_input" {
		t.Fatalf("badpdf: %+v", j)
	}
}
