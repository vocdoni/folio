// SPDX-License-Identifier: AGPL-3.0-only

package money

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func d(t *testing.T, s string) *Decimal {
	t.Helper()
	v, err := Parse(s, 4)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func eur(t *testing.T) Currency {
	c, ok := LookupCurrency("EUR")
	if !ok {
		t.Fatal("EUR missing")
	}
	return c
}

func TestParseStringAndNumberAgree(t *testing.T) {
	a, err := Parse("1234.5600", 4)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(json.Number("1234.56"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(a, b) {
		t.Fatalf("%s != %s", a, b)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []any{"-1", "1.23456", "abc", "NaN", "Infinity", "1e13", true, 1.5, nil} {
		if _, err := Parse(in, 4); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%v): err = %v, want ErrInvalid", in, err)
		}
	}
	if _, err := Parse("10.123", 2); err == nil {
		t.Error("3 decimals accepted with max 2")
	}
}

func TestRoundHalfUp(t *testing.T) {
	cases := map[string]string{"0.005": "0.01", "0.015": "0.02", "2.675": "2.68", "1.004": "1.00", "0.125": "0.13"}
	for in, want := range cases {
		got, err := Round(d(t, in), 2)
		if err != nil {
			t.Fatal(err)
		}
		if got.Text('f') != want {
			t.Errorf("Round(%s) = %s, want %s", in, got.Text('f'), want)
		}
	}
	jpy, _ := LookupCurrency("JPY")
	got, _ := Round(d(t, "100.5"), jpy.MinorUnits)
	if got.Text('f') != "101" {
		t.Errorf("JPY round = %s", got.Text('f'))
	}
}

func TestComputeBudgetTaxGroupsAndDiscounts(t *testing.T) {
	b := Budget{
		Currency:    eur(t),
		TaxRate:     d(t, "21"),
		DiscountPct: d(t, "10"),
		Items: []Item{
			{Quantity: d(t, "3"), UnitPrice: d(t, "33.335")},                         // 100.005 -> 100.01
			{Quantity: d(t, "1"), UnitPrice: d(t, "200"), DiscountPct: d(t, "12.5")}, // 175.00
			{Quantity: d(t, "2"), UnitPrice: d(t, "19.99"), TaxRate: d(t, "10")},     // 39.98
			{Quantity: d(t, "1.5"), UnitPrice: d(t, "10"), TaxRate: d(t, "0")},       // 15.00
		},
	}
	tot, err := ComputeBudget(b)
	if err != nil {
		t.Fatal(err)
	}
	// line nets
	nets := []string{"100.01", "175.00", "39.98", "15.00"}
	for i, n := range nets {
		if tot.LineNets[i].Text('f') != n {
			t.Errorf("net[%d] = %s, want %s", i, tot.LineNets[i].Text('f'), n)
		}
	}
	// document discount 10% per line: 90.009->90.01, 157.50, 35.982->35.98, 13.50
	disc := []string{"90.01", "157.50", "35.98", "13.50"}
	for i, n := range disc {
		if tot.LineDiscounted[i].Text('f') != n {
			t.Errorf("discounted[%d] = %s, want %s", i, tot.LineDiscounted[i].Text('f'), n)
		}
	}
	check := func(name string, got *Decimal, want string) {
		t.Helper()
		if got.Text('f') != want {
			t.Errorf("%s = %s, want %s", name, got.Text('f'), want)
		}
	}
	check("subtotal", tot.Subtotal, "329.99")
	check("discounted subtotal", tot.DiscountedSubtotal, "296.99")
	check("discount amount", tot.DiscountAmount, "33.00")
	if len(tot.TaxGroups) != 3 {
		t.Fatalf("tax groups = %d, want 3", len(tot.TaxGroups))
	}
	// rate 0: base 13.50 tax 0.00; rate 10: base 35.98 tax 3.598->3.60; rate 21: base 247.51 tax 51.9771->51.98
	check("g0.rate", tot.TaxGroups[0].Rate, "0")
	check("g1.tax", tot.TaxGroups[1].Tax, "3.60")
	check("g2.base", tot.TaxGroups[2].Base, "247.51")
	check("g2.tax", tot.TaxGroups[2].Tax, "51.98")
	check("tax total", tot.TaxTotal, "55.58")
	check("total", tot.Total, "352.57")
}

func TestSplitPaymentsRemainder(t *testing.T) {
	total := d(t, "100.00")
	pcts := []*Decimal{d(t, "33.33"), d(t, "33.33"), d(t, "33.34")}
	got, err := SplitPayments(total, pcts, eur(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"33.33", "33.33", "33.34"}
	for i := range want {
		if got[i].Text('f') != want[i] {
			t.Errorf("payment[%d] = %s, want %s", i, got[i].Text('f'), want[i])
		}
	}
	got, _ = SplitPayments(d(t, "10.00"), []*Decimal{d(t, "33.33"), d(t, "33.33"), d(t, "33.34")}, eur(t))
	// 3.333->3.33, 3.33, last = 10.00-6.66 = 3.34
	if got[2].Text('f') != "3.34" {
		t.Errorf("last payment = %s, want 3.34", got[2].Text('f'))
	}
	if _, err := SplitPayments(total, []*Decimal{d(t, "50"), d(t, "49.99")}, eur(t)); !errors.Is(err, ErrInvalid) {
		t.Errorf("sum 99.99 accepted: %v", err)
	}
}

func TestFormatAmount(t *testing.T) {
	usd, _ := LookupCurrency("USD")
	chf, _ := LookupCurrency("CHF")
	cases := []struct {
		amount string
		cur    Currency
		lang   string
		want   string
	}{
		{"1234.56", eur(t), "en", "€1,234.56"},
		{"1234.56", eur(t), "es", "1.234,56 €"},
		{"1234.56", eur(t), "ca", "1.234,56 €"},
		{"1234567.5", usd, "en", "$1,234,567.50"},
		{"1234.56", chf, "en", "CHF 1,234.56"},
		{"1234.56", chf, "es", "1.234,56 CHF"},
		{"999", eur(t), "fr", "€999.00"},
		{"0.5", eur(t), "en", "€0.50"},
	}
	for _, c := range cases {
		got, err := FormatAmount(d(t, c.amount), c.cur, c.lang)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("FormatAmount(%s,%s,%s) = %q, want %q", c.amount, c.cur.Code, c.lang, got, c.want)
		}
	}
	if got := FormatPercent(d(t, "21"), "es"); got != "21 %" {
		t.Errorf("percent es = %q", got)
	}
	if got := FormatNumber(d(t, "1234.5"), "es"); got != "1.234,5" {
		t.Errorf("number es = %q", got)
	}
}

// TestNoFloatOnMoneyPath enforces the "no float64 for money" rule on this
// package's non-test sources (backstop for the forbidigo lint rule).
func TestNoFloatOnMoneyPath(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "float64" || x.Name == "float32" {
					t.Errorf("%s: %s is forbidden in internal/money", fset.Position(x.Pos()), x.Name)
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "ParseFloat" || x.Sel.Name == "Float64" {
					t.Errorf("%s: %s is forbidden in internal/money", fset.Position(x.Pos()), x.Sel.Name)
				}
			}
			return true
		})
	}
}
