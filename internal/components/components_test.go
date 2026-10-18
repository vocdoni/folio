// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	v, err := model.DecodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

var labelsEN = map[string]string{
	"budget.description": "Description", "budget.quantity": "Qty", "budget.unit": "Unit",
	"budget.unit_price": "Unit price", "budget.discount": "Discount", "budget.amount": "Amount",
	"milestones.name": "Milestone", "milestones.date": "When", "milestones.description": "Description",
	"milestones.deliverables": "Deliverables", "milestones.payment": "Payment",
}

// cellText is the text of a table cell exactly as the Str/Space nodes carry it.
func cellText(c pandocast.Cell) string {
	if len(c.Blocks) == 0 {
		return ""
	}
	return pandocast.Stringify(c.Blocks[0].(*pandocast.Plain).Inlines)
}

func rowTexts(r pandocast.Row) []string {
	var out []string
	for _, c := range r.Cells {
		out = append(out, cellText(c))
	}
	return out
}

func TestBudgetRendersValuesVerbatim(t *testing.T) {
	data := decode(t, `{"budget": {"items": [
	  {"description": "Design", "quantity": "≈ 40 h", "unit": "hour", "unit_price": "TBD", "discount": "-10 %", "amount": "1.234,5 EUR"},
	  {"description": "Hosting", "amount": "€1,234.50"}],
	 "totals": [{"label": "Subtotal", "amount": "2.469,00 EUR"}, {"label": "VAT (21%)", "amount": "incl."},
	            {"label": "Total", "amount": "€ 2.469,00", "emphasis": true}],
	 "notes": "Prices are indicative."}}`)
	blocks, err := Expand(Use{Name: "budget"}, data, Context{Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	tbl := blocks[0].(*pandocast.Table)
	if got := strings.Join(rowTexts(tbl.Head.Rows[0]), "|"); got != "Description|Qty|Unit|Unit price|Discount|Amount" {
		t.Fatalf("head = %s", got)
	}
	if got := strings.Join(rowTexts(tbl.Bodies[0].Body[0]), "|"); got != "Design|≈ 40 h|hour|TBD|-10 %|1.234,5 EUR" {
		t.Fatalf("row 1 = %s", got)
	}
	if got := strings.Join(rowTexts(tbl.Bodies[0].Body[1]), "|"); got != "Hosting|||||€1,234.50" {
		t.Fatalf("row 2 = %s", got)
	}
	foot := tbl.Foot.Rows
	if len(foot) != 3 || foot[0].Cells[0].ColSpan != 5 || cellText(foot[1].Cells[0]) != "VAT (21%)" || cellText(foot[1].Cells[1]) != "incl." {
		t.Fatalf("foot = %+v", foot)
	}
	total := foot[2].Cells[1].Blocks[0].(*pandocast.Plain).Inlines[0].(*pandocast.Strong)
	if pandocast.Stringify(total.Inlines) != "€ 2.469,00" {
		t.Fatalf("emphasised total = %q", pandocast.Stringify(total.Inlines))
	}
	if _, plainLabel := foot[0].Cells[0].Blocks[0].(*pandocast.Plain).Inlines[0].(*pandocast.Str); !plainLabel {
		t.Fatal("rows without emphasis must not be bold")
	}
	if notes := blocks[1].(*pandocast.Para); pandocast.Stringify(notes.Inlines) != "Prices are indicative." {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestBudgetColumnsFollowTheData(t *testing.T) {
	data := decode(t, `{"budget": {"items": [{"description": "x", "amount": "10"}, {"description": "y", "unit_price": "5", "amount": "5"}]}}`)
	blocks, err := Expand(Use{Name: "budget"}, data, Context{Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	tbl := blocks[0].(*pandocast.Table)
	if got := strings.Join(rowTexts(tbl.Head.Rows[0]), "|"); got != "Description|Unit price|Amount" || len(tbl.ColSpecs) != 3 {
		t.Fatalf("head = %s", got)
	}
	if len(tbl.Foot.Rows) != 0 || len(blocks) != 1 {
		t.Fatalf("no totals and no notes were sent: %+v", tbl.Foot.Rows)
	}
}

func TestMilestonePaymentShownVerbatim(t *testing.T) {
	data := decode(t, `{"milestones": [{"name": "Kick-off", "date": "2026-11-02", "payment": "30% · €6,635.64"}, {"name": "Launch", "payment": "TBD"}]}`)
	blocks, err := Expand(Use{Name: "milestones"}, data, Context{Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	tbl := blocks[0].(*pandocast.Table)
	if got := strings.Join(rowTexts(tbl.Head.Rows[0]), "|"); got != "Milestone|When|Payment" || tbl.ColSpecs[2].Align != "AlignRight" {
		t.Fatalf("head = %s", got)
	}
	if got := cellText(tbl.Bodies[0].Body[0].Cells[2]); got != "30% · €6,635.64" {
		t.Fatalf("payment = %q", got)
	}
}

// The "When" cell joins date and duration without trimming Unicode white
// space: a no-break space at the start of a duration survives.
func TestMilestoneWhenKeepsNoBreakSpace(t *testing.T) {
	data := decode(t, `{"milestones": [{"name": "A", "duration": "\u00a02 weeks"}, {"name": "B", "date": "2026-01-01", "duration": "2 w"}]}`)
	blocks, err := Expand(Use{Name: "milestones"}, data, Context{Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	body := blocks[0].(*pandocast.Table).Bodies[0].Body
	if got := cellText(body[0].Cells[1]); got != "\u00a02 weeks" {
		t.Fatalf("when = %q", got)
	}
	if got := cellText(body[1].Cells[1]); got != "2026-01-01 2 w" {
		t.Fatalf("when = %q", got)
	}
}

func TestEmptyColumnsOmitted(t *testing.T) {
	data := decode(t, `{"milestones": [{"name": "A", "date": "2026-01-01"}, {"name": "B", "duration": "2w"}],
	 "deliverables": [{"id": "D1", "name": "Spec"}, {"id": "D2", "name": "Code", "due": "May"}]}`)
	ctx := Context{Labels: labelsEN}
	blocks, err := Expand(Use{Name: "milestones"}, data, ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := blocks[0].(*pandocast.Table)
	if len(m.ColSpecs) != 2 || len(m.Head.Rows[0].Cells) != 2 || len(m.Bodies[0].Body[0].Cells) != 2 {
		t.Fatalf("milestones columns = %d", len(m.ColSpecs))
	}
	blocks, err = Expand(Use{Name: "deliverables"}, data, ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := blocks[0].(*pandocast.Table) // id, name, due remain
	if len(d.ColSpecs) != 3 || len(d.Head.Rows[0].Cells) != 3 || len(d.Bodies[0].Body[1].Cells) != 3 {
		t.Fatalf("deliverables columns = %d", len(d.ColSpecs))
	}
}

func TestInfoAndSignaturesHaveNoHeader(t *testing.T) {
	ctx := Context{Labels: labelsEN, Metadata: map[string]any{"client": "ACME", "date": "2026-01-01"}}
	info, _ := Expand(Use{Name: "info"}, nil, ctx)
	sigs, err := Expand(Use{Name: "signatures"}, decode(t, `{"signatures": [{"name": "Ann"}, {"name": "Bob"}]}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range append(info, sigs...) {
		if tbl := b.(*pandocast.Table); len(tbl.Head.Rows) != 0 {
			t.Fatalf("table %v has header rows", tbl.Attr)
		}
	}
}

func TestExpandBudgetIsInertText(t *testing.T) {
	data := decode(t, `{"budget": {"items": [{"description": "#read(\"/etc/passwd\") *x* <b>", "amount": "$1 \\$2 [link](x)"}]}}`)
	blocks, err := Expand(Use{Name: "budget"}, data, Context{Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	row := blocks[0].(*pandocast.Table).Bodies[0].Body[0]
	for _, c := range row.Cells {
		for _, in := range c.Blocks[0].(*pandocast.Plain).Inlines {
			switch in.(type) {
			case *pandocast.Str, *pandocast.Space:
			default:
				t.Fatalf("cell produced %T", in)
			}
		}
	}
	if got := cellText(row.Cells[1]); got != `$1 \$2 [link](x)` {
		t.Fatalf("amount = %q", got)
	}
}

// Only ASCII white space separates words: no-break and narrow no-break spaces
// stay inside the value, so such an amount is never split across lines.
func TestUnicodeSpacesStayInsideValues(t *testing.T) {
	v := "1\u202f234,50\u00a0€" // narrow no-break and no-break spaces
	data := decode(t, `{"budget": {"items": [{"description": "x", "amount": "`+v+`"}]}}`)
	blocks, _ := Expand(Use{Name: "budget"}, data, Context{Labels: labelsEN})
	ins := blocks[0].(*pandocast.Table).Bodies[0].Body[0].Cells[1].Blocks[0].(*pandocast.Plain).Inlines
	if len(ins) != 1 || ins[0].(*pandocast.Str).Text != v {
		t.Fatalf("inlines = %+v", ins)
	}
}

func TestCheckUnknownDeliverable(t *testing.T) {
	data := decode(t, `{"deliverables": [{"id": "D1", "name": "Spec"}, {"id": "D2", "name": "Code"}],
	 "milestones": [{"name": "A", "deliverables": ["D1"]}, {"name": "B", "deliverables": ["D2", "D9"]}]}`)
	fs := Check(data, []Use{{Name: "milestones"}, {Name: "deliverables"}})
	if len(fs) != 1 || fs[0].Code != model.CodeUnknownDeliverable || fs[0].Path != "/data/milestones/1/deliverables/1" ||
		fs[0].Severity != model.SeverityError || !strings.Contains(fs[0].Hint, "D1, D2") {
		t.Fatalf("findings = %+v", fs)
	}
	if fs := Check(data, []Use{{Name: "milestones"}}); len(fs) != 0 {
		t.Fatalf("without the deliverables component ids are free text: %+v", fs)
	}
}

// TestNoNumericParsing enforces that components never parse or compute
// figures: no math or decimal packages, no strconv parsing, no floats.
func TestNoNumericParsing(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenImports := map[string]bool{"math": true, "math/big": true, "github.com/cockroachdb/apd/v3": true,
		"github.com/vocdoni/folio/internal/money": true}
	forbiddenCalls := map[string]bool{"ParseFloat": true, "ParseInt": true, "ParseUint": true, "Atoi": true}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbiddenImports[p] || strings.HasPrefix(p, "math/") {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "strconv" && forbiddenCalls[x.Sel.Name] {
					t.Errorf("%s calls strconv.%s", name, x.Sel.Name)
				}
			case *ast.Ident:
				if x.Name == "float32" || x.Name == "float64" {
					t.Errorf("%s uses %s", name, x.Name)
				}
			}
			return true
		})
	}
	if checked < 3 {
		t.Fatalf("checked %d files", checked)
	}
}
