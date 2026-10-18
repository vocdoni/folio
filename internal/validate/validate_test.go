// SPDX-License-Identifier: AGPL-3.0-only

package validate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
)

var limits = Limits{MaxMarkdownBytes: 1 << 20, MaxNesting: 32, MaxTableCells: 10000}

func tmpl() model.Template {
	return model.Template{
		ID: "proposal", Languages: []string{"en", "es"},
		Components: []string{"budget", "info"},
		Sections: []model.Section{
			{Title: "Executive summary", Required: true, Aliases: []string{"Summary"}},
			{Title: "Budget", Required: true, Component: "budget"},
		},
		MetadataSchema: json.RawMessage(`{"properties":{"client":{"minLength":2}}}`),
	}
}

func has(fs []model.Finding, code string, sev model.Severity) bool {
	for _, f := range fs {
		if f.Code == code && f.Severity == sev {
			return true
		}
	}
	return false
}

// AST fixtures are built directly so these tests do not need pandoc.
func header(text string) pandocast.Block {
	return &pandocast.Header{Level: 1, Attr: pandocast.NewAttr(""), Inlines: pandocast.Text(text)}
}
func para(text string) pandocast.Block { return &pandocast.Para{Inlines: pandocast.Text(text)} }
func comp(name string, blocks ...pandocast.Block) pandocast.Block {
	if blocks == nil {
		blocks = []pandocast.Block{}
	}
	return &pandocast.Div{Attr: pandocast.NewAttr("", "folio-"+name), Blocks: blocks}
}

func TestValidationMissingSectionAndAlias(t *testing.T) {
	doc := &pandocast.Document{Blocks: []pandocast.Block{header("Summary"), para("ok")}}
	r := CheckAST(doc, tmpl(), limits, nil)
	if !has(r.Findings, model.CodeMissingSection, model.SeverityError) {
		t.Fatalf("missing Budget not reported: %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Code == model.CodeMissingSection && strings.Contains(f.Message, "Executive summary") {
			t.Fatal("alias Summary did not satisfy Executive summary")
		}
	}
}

func TestValidationComponents(t *testing.T) {
	doc := &pandocast.Document{Blocks: []pandocast.Block{
		header("Executive summary"), comp("budget"), // misplaced: budget belongs in Budget
		header("Budget"), comp("budget"),
		comp("nosuch"), comp("milestones"), comp("info", para("x")),
		&pandocast.BlockQuote{Blocks: []pandocast.Block{comp("info")}},
	}}
	r := CheckAST(doc, tmpl(), limits, nil)
	for _, code := range []string{model.CodeMisplacedComponent, model.CodeUnknownComponent, model.CodeComponentNotAllowed, model.CodeComponentNotEmpty} {
		if !has(r.Findings, code, model.SeverityError) {
			t.Errorf("%s not reported: %+v", code, r.Findings)
		}
	}
	if len(r.Uses) != 1 || r.Uses[0].Name != "budget" || r.Uses[0].Index != 3 {
		t.Errorf("uses = %+v", r.Uses)
	}
}

func TestSecurityImageTargetsAndLinks(t *testing.T) {
	img := func(u string) pandocast.Inline {
		return &pandocast.Image{Attr: pandocast.NewAttr(""), Inlines: []pandocast.Inline{}, Target: pandocast.Target{URL: u}}
	}
	link := func(u string) pandocast.Inline {
		return &pandocast.Link{Attr: pandocast.NewAttr(""), Inlines: []pandocast.Inline{}, Target: pandocast.Target{URL: u}}
	}
	ok := "asset:0192abcd-0000-7000-8000-000000000000"
	gone := "asset:0192abcd-0000-7000-8000-00000000dead"
	doc := &pandocast.Document{Blocks: []pandocast.Block{&pandocast.Para{Inlines: []pandocast.Inline{
		img(ok), img(gone), img("https://evil.example/x.png"), img("/etc/passwd"), img("assets/x.png"), img("file:///etc/passwd"),
		link("https://example.com"), link("mailto:a@b.c"), link("#scope"), link("file:///etc/passwd"), link("javascript:alert(1)"),
	}}}}
	r := CheckAST(doc, model.Template{}, limits, func(id string) bool { return !strings.HasSuffix(id, "dead") })
	n := 0
	for _, f := range r.Findings {
		if f.Code == model.CodeInvalidImage {
			n++
		}
	}
	if n != 4 {
		t.Errorf("invalid image findings = %d, want 4: %+v", n, r.Findings)
	}
	if !has(r.Findings, model.CodeMissingAsset, model.SeverityError) {
		t.Error("deleted asset not reported")
	}
	links := 0
	for _, f := range r.Findings {
		if f.Code == model.CodeInvalidLink {
			links++
		}
	}
	if links != 2 {
		t.Errorf("invalid links = %d, want 2", links)
	}
	if len(r.AssetIDs) != 1 {
		t.Errorf("asset ids = %v", r.AssetIDs)
	}
}

func TestSecurityRawNodesRejected(t *testing.T) {
	doc := &pandocast.Document{Blocks: []pandocast.Block{
		&pandocast.RawBlock{Format: "typst", Text: `#read("/etc/passwd")`},
		&pandocast.Para{Inlines: []pandocast.Inline{&pandocast.RawInline{Format: "html", Text: "<b>"}}},
	}}
	r := CheckAST(doc, model.Template{}, limits, nil)
	if !has(r.Findings, model.CodeRawContent, model.SeverityError) {
		t.Fatal("raw nodes accepted")
	}
}

func TestValidationPlaceholdersAndStrict(t *testing.T) {
	doc := &pandocast.Document{Blocks: []pandocast.Block{para("Price: TBD"), para("Dear {{client}}"),
		&pandocast.Para{Inlines: []pandocast.Inline{&pandocast.Code{Attr: pandocast.NewAttr(""), Text: "TODO in code is fine"}}}}}
	r := CheckAST(doc, model.Template{}, limits, nil)
	if !has(r.Findings, model.CodePlaceholder, model.SeverityWarning) || has(r.Findings, model.CodePlaceholder, model.SeverityError) {
		t.Fatalf("non-strict: %+v", r.Findings)
	}
	n := 0
	for _, f := range r.Findings {
		if f.Code == model.CodePlaceholder {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("placeholder findings = %d, want 2 (code excluded)", n)
	}
	r = CheckAST(doc, model.Template{Strict: true}, limits, nil)
	if !has(r.Findings, model.CodePlaceholder, model.SeverityError) {
		t.Fatal("strict did not upgrade placeholders")
	}
}

func TestValidationNestingAndTables(t *testing.T) {
	b := para("deep")
	for i := 0; i < 40; i++ {
		b = &pandocast.BlockQuote{Blocks: []pandocast.Block{b}}
	}
	r := CheckAST(&pandocast.Document{Blocks: []pandocast.Block{b}}, model.Template{}, limits, nil)
	if !has(r.Findings, model.CodeNestingTooDeep, model.SeverityError) {
		t.Fatal("deep nesting accepted")
	}
}

func TestSecurityNestingBombRejectedBeforePandoc(t *testing.T) {
	bomb := strings.Repeat("> ", 20000) + "x\n"
	fs := PreScan(bomb, limits)
	if !has(fs, model.CodeNestingTooDeep, model.SeverityError) {
		t.Fatal("blockquote bomb passed the pre-scan")
	}
	list := ""
	for i := 0; i < 200; i++ {
		list += strings.Repeat("  ", i) + "- x\n"
	}
	if !has(PreScan(list, limits), model.CodeNestingTooDeep, model.SeverityError) {
		t.Fatal("list bomb passed the pre-scan")
	}
	for _, marker := range []string{"- ", "* ", "+ ", "1. ", "1) ", "> - "} {
		if !has(PreScan(strings.Repeat(marker, 20000)+"x\n", limits), model.CodeNestingTooDeep, model.SeverityError) {
			t.Fatalf("inline %q marker bomb passed the pre-scan", marker)
		}
	}
	normal := "# T\n\n> quote\n\n- a\n  - b\n    - c\n\n1. one\n   - 1.1\n\n* * *\n\n```\n" + strings.Repeat("> ", 100) + "\n```\n"
	if fs := PreScan(normal, limits); len(fs) != 0 {
		t.Fatalf("normal markdown flagged: %+v", fs)
	}
	if !has(PreScan(strings.Repeat("x", 2<<20), limits), model.CodeMarkdownTooLarge, model.SeverityError) {
		t.Fatal("size limit not enforced")
	}
}

// Each prefix makes the pre-scan believe a code fence is open while pandoc
// parses what follows as ordinary Markdown (verified against pandoc): an
// unclosed fence, a one-line ```code```, a tab-indented fence (indented code
// to pandoc) and a ```go line that does not close the open block (so the
// real closer reopens a fence in a naive scan).
func TestSecurityNestingBombBehindFakeFence(t *testing.T) {
	bomb := strings.Repeat("> ", 20000) + "x\n"
	for name, prefix := range map[string]string{
		"unclosed":    "```\n\n",
		"inline":      "```foo```\n\n",
		"tab":         "\t```\n\n",
		"info closer": "```\na\n```go\n```\n\n",
	} {
		if !has(PreScan(prefix+bomb, limits), model.CodeNestingTooDeep, model.SeverityError) {
			t.Errorf("%s: blockquote bomb behind a fake fence passed the pre-scan", name)
		}
		divs := prefix + strings.Repeat("::: a\n", 20000)
		if !has(PreScan(divs, limits), model.CodeNestingTooDeep, model.SeverityError) {
			t.Errorf("%s: div bomb behind a fake fence passed the pre-scan", name)
		}
	}
}

func TestValidationMalformedPipeTable(t *testing.T) {
	md := "| A | B | C |\n|---|---|---|\n| 1 | 2 |\n| 3 | `a|b` | 5 |\n| 6 | 7 | 8 | 9 |\n"
	fs := PreScan(md, limits)
	n := 0
	for _, f := range fs {
		if f.Code == model.CodeMalformedTable {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("malformed rows = %d, want 2 (lines 3 and 5): %+v", n, fs)
	}
}

func TestValidationSchemaPointers(t *testing.T) {
	s, fs := CompileSchemas(tmpl())
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	meta, _ := model.DecodeJSON([]byte(`{"title": "", "date": "2026-02-30", "language": "fr", "client": "x"}`))
	got := s.ValidateMetadata(meta)
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
	}
	for _, p := range []string{"/metadata/title", "/metadata/language", "/metadata/client"} {
		if !paths[p] {
			t.Errorf("no finding at %s: %+v", p, got)
		}
	}
	meta, _ = model.DecodeJSON([]byte(`{"title": "T", "date": "2026-02-30", "language": "en"}`))
	if got := s.ValidateMetadata(meta); len(got) != 1 || got[0].Path != "/metadata/date" {
		t.Errorf("calendar date not checked: %+v", got)
	}
}

func TestSecuritySchemaRefsCannotEscape(t *testing.T) {
	for _, ref := range []string{"file:///etc/passwd", "https://example.com/s.json"} {
		tp := tmpl()
		tp.DataSchema = json.RawMessage(`{"$ref":"` + ref + `"}`)
		if _, fs := CompileSchemas(tp); len(fs) == 0 {
			t.Errorf("$ref %s compiled", ref)
		}
	}
	tp := tmpl()
	tp.DataSchema = json.RawMessage(`{"type":"object","properties":{"budget":{"$ref":"folio:component/budget"}}}`)
	if _, fs := CompileSchemas(tp); len(fs) != 0 {
		t.Errorf("folio:component ref failed: %+v", fs)
	}
}

// Component divs take no attributes: a stale `show_payments=true` (or any
// other key=value) is an error that names the new data field.
func TestValidationComponentAttributes(t *testing.T) {
	div := comp("budget").(*pandocast.Div)
	div.Attr.KeyVals = [][2]string{{"show_payments", "true"}}
	doc := &pandocast.Document{Blocks: []pandocast.Block{header("Executive summary"), para("ok"), header("Budget"), div}}
	r := CheckAST(doc, tmpl(), limits, nil)
	if len(r.Findings) != 1 || r.Findings[0].Code != model.CodeComponentAttribute || r.Findings[0].Severity != model.SeverityError ||
		r.Findings[0].Hint != "remove the attribute; milestone payments are now `payment` strings in data.milestones" ||
		!strings.Contains(r.Findings[0].Message, "show_payments=true") {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

// A JSON number where a component expects a string fails with the figure hint,
// once, even when the template data_schema references the component schema.
func TestValidationNumbersInFiguresGetTheHint(t *testing.T) {
	tp := tmpl()
	tp.DataSchema = json.RawMessage(`{"type":"object","properties":{"budget":{"$ref":"folio:component/budget"}}}`)
	s, fs := CompileSchemas(tp)
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	data, _ := model.DecodeJSON([]byte(`{"budget": {"items": [{"description": "Design", "quantity": 2, "amount": "€1,234.50"}],
	  "totals": [{"label": "Total", "amount": 1234.5}]}}`))
	got := s.ValidateData(data.(map[string]any), []components.Use{{Name: "budget"}})
	want := map[string]bool{"/data/budget/items/0/quantity": false, "/data/budget/totals/0/amount": false}
	for _, f := range got {
		if _, ok := want[f.Path]; !ok || f.Code != model.CodeSchemaViolation || f.Hint != FigureHint || want[f.Path] {
			t.Fatalf("unexpected or repeated finding %+v in %+v", f, got)
		}
		want[f.Path] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("no finding at %s: %+v", p, got)
		}
	}
	if FigureHint != `send figures as strings exactly as they should appear (e.g. "€1,234.50")` {
		t.Fatalf("hint = %q", FigureHint)
	}
	meta, _ := model.DecodeJSON([]byte(`{"title": 5, "date": "2026-10-18", "language": "en"}`))
	if mf := s.ValidateMetadata(meta); len(mf) != 1 || mf[0].Hint != "" {
		t.Fatalf("metadata findings carry no figure hint: %+v", mf)
	}
}

// A white-space-only figure or label satisfies minLength but not the schema.
func TestValidationBlankFigureRejected(t *testing.T) {
	tp := tmpl()
	tp.DataSchema = json.RawMessage(`{"type":"object","properties":{"budget":{"$ref":"folio:component/budget"}}}`)
	s, fs := CompileSchemas(tp)
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	data, _ := model.DecodeJSON([]byte(`{"budget": {"items": [{"description": "Design", "amount": " "}],
	  "totals": [{"label": "  ", "amount": "10"}]}}`))
	got := s.ValidateData(data.(map[string]any), []components.Use{{Name: "budget"}})
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
	}
	if !paths["/data/budget/items/0/amount"] || !paths["/data/budget/totals/0/label"] {
		t.Fatalf("blank figure/label accepted: %+v", got)
	}
}
