// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
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
	"budget.subtotal": "Subtotal", "budget.tax": "Tax", "budget.total": "Total",
	"milestones.payment": "Payment",
}

const budgetData = `{"budget": {"currency": "EUR", "tax_rate": "21", "discount_pct": 10, "items": [
  {"description": "Design", "quantity": 3, "unit_price": "33.335"},
  {"description": "Build", "quantity": "1", "unit_price": 200, "discount_pct": "12.5"},
  {"description": "Hosting", "quantity": 2, "unit_price": 19.99, "tax_rate": 10},
  {"description": "Support", "quantity": "1.5", "unit_price": "10", "tax_rate": "0"}]},
 "milestones": [{"name": "Kick-off", "payment_pct": "33.33"}, {"name": "Beta", "payment_pct": 33.33}, {"name": "Launch", "payment_pct": "33.34"}]}`

func TestBudgetComputeStringsAndNumbersAgree(t *testing.T) {
	uses := []Use{{Name: "budget"}, {Name: "milestones", Attrs: map[string]string{"show_payments": "true"}}}
	c, fs := Compute(decode(t, budgetData), uses, Context{Lang: "en", Labels: labelsEN})
	if len(fs) != 0 {
		t.Fatalf("findings: %+v", fs)
	}
	if c.Budget.Total != "352.57" || c.Budget.Subtotal != "329.99" || c.Budget.DiscountAmount != "33.00" {
		t.Fatalf("budget = %+v", c.Budget)
	}
	if got := c.Milestones.Items[2].Amount; got != "117.55" { // 352.57×33.33% = 117.5116 → 117.51 twice; last = 352.57 − 235.02
		t.Fatalf("last payment = %s", got)
	}
}

func TestBudgetFindingsCarryPointers(t *testing.T) {
	data := decode(t, `{"budget": {"currency": "EUR", "items": [{"description": "x", "quantity": "-1", "unit_price": "1.23456"}], "expected_total": "1"}}`)
	_, fs := Compute(data, []Use{{Name: "budget"}}, Context{Lang: "en"})
	paths := map[string]bool{}
	for _, f := range fs {
		paths[f.Path] = true
	}
	for _, p := range []string{"/data/budget/items/0/quantity", "/data/budget/items/0/unit_price"} {
		if !paths[p] {
			t.Errorf("missing finding at %s: %+v", p, fs)
		}
	}
}

func TestBudgetExpectedTotalMismatch(t *testing.T) {
	data := decode(t, `{"budget": {"currency": "EUR", "items": [{"description": "x", "quantity": 1, "unit_price": "100"}], "tax_rate": 21, "expected_total": "120"}}`)
	_, fs := Compute(data, []Use{{Name: "budget"}}, Context{Lang: "en"})
	if len(fs) != 1 || fs[0].Code != model.CodeExpectedTotal || !strings.Contains(fs[0].Message, "121.00") || !strings.Contains(fs[0].Message, "120") {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestPaymentSumMustBe100(t *testing.T) {
	data := decode(t, `{"budget": {"currency": "EUR", "items": [{"description": "x", "quantity": 1, "unit_price": "100"}]},
	  "milestones": [{"name": "a", "payment_pct": 50}, {"name": "b", "payment_pct": 49}]}`)
	_, fs := Compute(data, []Use{{Name: "budget"}, {Name: "milestones", Attrs: map[string]string{"show_payments": "true"}}}, Context{Lang: "en"})
	if len(fs) != 1 || fs[0].Code != model.CodePaymentSum {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestExpandBudgetIsInertText(t *testing.T) {
	data := decode(t, `{"budget": {"currency": "EUR", "tax_rate": 21, "items": [{"description": "#read(\"/etc/passwd\") *x*", "quantity": 1, "unit_price": "1234.5"}]}}`)
	c, fs := Compute(data, []Use{{Name: "budget"}}, Context{Lang: "es", Labels: labelsEN})
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	blocks, err := Expand(Use{Name: "budget"}, data, c, Context{Lang: "es", Labels: labelsEN})
	if err != nil {
		t.Fatal(err)
	}
	tbl := blocks[0].(*pandocast.Table)
	desc := tbl.Bodies[0].Body[0].Cells[0].Blocks[0].(*pandocast.Plain).Inlines
	for _, in := range desc {
		if _, ok := in.(*pandocast.Str); !ok {
			if _, ok := in.(*pandocast.Space); !ok {
				t.Fatalf("description produced %T", in)
			}
		}
	}
	last := tbl.Foot.Rows[len(tbl.Foot.Rows)-1]
	total := pandocast.Stringify(last.Cells[1].Blocks[0].(*pandocast.Plain).Inlines[0].(*pandocast.Strong).Inlines)
	if total != "1.493,75 €" { // 1234.50 + 21% = 1493.745 → 1493.75 (tax 259.25 rounded half-up)
		t.Fatalf("total = %q", total)
	}
}
