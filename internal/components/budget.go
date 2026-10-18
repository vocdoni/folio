// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/money"
	"github.com/vocdoni/folio/internal/pandocast"
)

// BudgetComputed is the stored result of the budget algorithm. Amounts are
// decimal strings rounded to the currency's minor unit.
type BudgetComputed struct {
	Currency           string           `json:"currency"`
	Lines              []BudgetLine     `json:"lines"`
	Subtotal           string           `json:"subtotal"`
	DiscountPct        string           `json:"discount_pct,omitempty"`
	DiscountAmount     string           `json:"discount_amount"`
	DiscountedSubtotal string           `json:"discounted_subtotal"`
	TaxGroups          []BudgetTaxGroup `json:"tax_groups"`
	TaxTotal           string           `json:"tax_total"`
	Total              string           `json:"total"`
	Notes              string           `json:"notes,omitempty"`
	in                 money.Budget     // parsed input, for formatting
	items              []map[string]any // raw items, for descriptions/units
	totals             money.Totals     // exact values
}

type BudgetLine struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit,omitempty"`
	UnitPrice   string `json:"unit_price"`
	DiscountPct string `json:"discount_pct,omitempty"`
	TaxRate     string `json:"tax_rate"`
	Net         string `json:"net"`
	Discounted  string `json:"discounted"`
}

type BudgetTaxGroup struct {
	Rate string `json:"rate"`
	Base string `json:"base"`
	Tax  string `json:"tax"`
}

func moneyErr(path, msg string) model.Finding {
	return model.Finding{Code: model.CodeMoneyInvalid, Severity: model.SeverityError, Path: path, Message: msg}
}

// parseField parses m[key] (string or json.Number) when present.
// Percentages must be ≤ 100.
func parseField(m map[string]any, key, path string, maxDec int, pct bool, fs *[]model.Finding) *money.Decimal {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	d, err := money.Parse(v, maxDec)
	if err != nil {
		*fs = append(*fs, moneyErr(path+"/"+key, fmt.Sprintf("%s: %v", key, err)))
		return nil
	}
	if pct && d.Cmp(money.Hundred()) > 0 {
		*fs = append(*fs, moneyErr(path+"/"+key, key+" must be between 0 and 100"))
		return nil
	}
	return d
}

func computeBudget(raw any, path string) (*BudgetComputed, []model.Finding) {
	var fs []model.Finding
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, []model.Finding{moneyErr(path, "data.budget is required by the budget component and must be an object")}
	}
	cur, ok := money.LookupCurrency(str(m, "currency"))
	if !ok {
		fs = append(fs, moneyErr(path+"/currency", fmt.Sprintf("unknown ISO 4217 currency %q", str(m, "currency"))))
	}
	b := money.Budget{
		Currency:    cur,
		TaxRate:     parseField(m, "tax_rate", path, 2, true, &fs),
		DiscountPct: parseField(m, "discount_pct", path, 2, true, &fs),
	}
	items, _ := m["items"].([]any)
	if len(items) == 0 {
		fs = append(fs, moneyErr(path+"/items", "items must contain at least one line"))
	}
	var rawItems []map[string]any
	for i, it := range items {
		im, ok := it.(map[string]any)
		ip := path + "/items/" + strconv.Itoa(i)
		if !ok {
			fs = append(fs, moneyErr(ip, "each item must be an object"))
			continue
		}
		item := money.Item{
			Quantity:    parseField(im, "quantity", ip, 4, false, &fs),
			UnitPrice:   parseField(im, "unit_price", ip, 4, false, &fs),
			DiscountPct: parseField(im, "discount_pct", ip, 2, true, &fs),
			TaxRate:     parseField(im, "tax_rate", ip, 2, true, &fs),
		}
		if item.Quantity == nil || item.UnitPrice == nil {
			if _, ok := im["quantity"]; !ok {
				fs = append(fs, moneyErr(ip+"/quantity", "quantity is required"))
			}
			if _, ok := im["unit_price"]; !ok {
				fs = append(fs, moneyErr(ip+"/unit_price", "unit_price is required"))
			}
			continue
		}
		b.Items = append(b.Items, item)
		rawItems = append(rawItems, im)
	}
	expected := parseField(m, "expected_total", path, 4, false, &fs)
	if len(fs) > 0 {
		return nil, fs
	}
	t, err := money.ComputeBudget(b)
	if err != nil {
		return nil, []model.Finding{moneyErr(path, err.Error())}
	}
	if expected != nil && !money.Equal(expected, t.Total) {
		return nil, []model.Finding{{Code: model.CodeExpectedTotal, Severity: model.SeverityError, Path: path + "/expected_total",
			Message: fmt.Sprintf("expected_total %s does not match the computed total %s", expected.Text('f'), t.Total.Text('f')),
			Hint:    "Folio computes totals; remove expected_total or fix the line items"}}
	}
	return newBudgetComputed(b, t, rawItems, str(m, "notes")), nil
}

func newBudgetComputed(b money.Budget, t money.Totals, raw []map[string]any, notes string) *BudgetComputed {
	txt := func(d *money.Decimal) string {
		if d == nil {
			return ""
		}
		return d.Text('f')
	}
	bc := &BudgetComputed{
		Currency: b.Currency.Code, Subtotal: txt(t.Subtotal), DiscountPct: txt(b.DiscountPct),
		DiscountAmount: txt(t.DiscountAmount), DiscountedSubtotal: txt(t.DiscountedSubtotal),
		TaxTotal: txt(t.TaxTotal), Total: txt(t.Total), Notes: notes,
		in: b, items: raw, totals: t,
	}
	for i, it := range b.Items {
		rate := it.TaxRate
		if rate == nil {
			rate = b.TaxRate
		}
		rateText := "0"
		if rate != nil {
			rateText = txt(rate)
		}
		bc.Lines = append(bc.Lines, BudgetLine{
			Description: str(raw[i], "description"), Quantity: txt(it.Quantity), Unit: str(raw[i], "unit"),
			UnitPrice: txt(it.UnitPrice), DiscountPct: txt(it.DiscountPct), TaxRate: rateText,
			Net: txt(t.LineNets[i]), Discounted: txt(t.LineDiscounted[i]),
		})
	}
	for _, g := range t.TaxGroups {
		bc.TaxGroups = append(bc.TaxGroups, BudgetTaxGroup{Rate: txt(g.Rate), Base: txt(g.Base), Tax: txt(g.Tax)})
	}
	return bc
}

// MarshalJSON is the default struct encoding; declared so unexported fields
// never leak and the shape stays stable.
func (b *BudgetComputed) MarshalJSON() ([]byte, error) {
	type alias BudgetComputed
	return json.Marshal((*alias)(b))
}

// expandBudget renders: Description | Qty | [Unit] | Unit price | [Disc.] | Amount,
// then foot rows Subtotal, [Discount (x%)], Tax (rate%) per non-zero group, Total,
// then the notes paragraph.
func expandBudget(b *BudgetComputed, ctx Context) ([]pandocast.Block, error) {
	cur := b.in.Currency
	amt := func(d *money.Decimal) (string, error) { return money.FormatAmount(d, cur, ctx.Lang) }
	hasUnit, hasDisc := false, false
	for _, l := range b.Lines {
		hasUnit = hasUnit || l.Unit != ""
		hasDisc = hasDisc || l.DiscountPct != ""
	}
	aligns := []string{"AlignLeft", "AlignRight"}
	head := []string{ctx.label("budget.description"), ctx.label("budget.quantity")}
	if hasUnit {
		aligns, head = append(aligns, "AlignLeft"), append(head, ctx.label("budget.unit"))
	}
	aligns, head = append(aligns, "AlignRight"), append(head, ctx.label("budget.unit_price"))
	if hasDisc {
		aligns, head = append(aligns, "AlignRight"), append(head, ctx.label("budget.discount"))
	}
	aligns, head = append(aligns, "AlignRight"), append(head, ctx.label("budget.amount"))
	n := len(aligns)

	var body []pandocast.Row
	for i, it := range b.in.Items {
		price, err := amt(it.UnitPrice)
		if err != nil {
			return nil, err
		}
		net, err := amt(b.totals.LineNets[i])
		if err != nil {
			return nil, err
		}
		cells := []pandocast.Cell{
			cell(plain(b.Lines[i].Description), "AlignDefault"),
			cell(plain(money.FormatNumber(it.Quantity, ctx.Lang)), "AlignDefault"),
		}
		if hasUnit {
			cells = append(cells, cell(plain(b.Lines[i].Unit), "AlignDefault"))
		}
		cells = append(cells, cell(plain(price), "AlignDefault"))
		if hasDisc {
			d := ""
			if it.DiscountPct != nil {
				d = money.FormatPercent(it.DiscountPct, ctx.Lang)
			}
			cells = append(cells, cell(plain(d), "AlignDefault"))
		}
		cells = append(cells, cell(plain(net), "AlignDefault"))
		body = append(body, row(cells...))
	}
	summary := func(label, value string, strong bool) pandocast.Row {
		lb, vb := plain(label), plain(value)
		if strong {
			lb, vb = strongPlain(label), strongPlain(value)
		}
		c := cell(lb, "AlignDefault")
		c.ColSpan = n - 1
		return row(c, cell(vb, "AlignDefault"))
	}
	var foot []pandocast.Row
	sub, err := amt(b.totals.Subtotal)
	if err != nil {
		return nil, err
	}
	foot = append(foot, summary(ctx.label("budget.subtotal"), sub, false))
	if b.in.DiscountPct != nil && !b.totals.DiscountAmount.IsZero() {
		neg := *b.totals.DiscountAmount
		neg.Negative = true
		d, err := amt(&neg)
		if err != nil {
			return nil, err
		}
		foot = append(foot, summary(fmt.Sprintf("%s (%s)", ctx.label("budget.discount"), money.FormatPercent(b.in.DiscountPct, ctx.Lang)), d, false))
	}
	for _, g := range b.totals.TaxGroups {
		if g.Rate.IsZero() {
			continue
		}
		tx, err := amt(g.Tax)
		if err != nil {
			return nil, err
		}
		foot = append(foot, summary(fmt.Sprintf("%s (%s)", ctx.label("budget.tax"), money.FormatPercent(g.Rate, ctx.Lang)), tx, false))
	}
	total, err := amt(b.totals.Total)
	if err != nil {
		return nil, err
	}
	foot = append(foot, summary(ctx.label("budget.total"), total, true))
	blocks := []pandocast.Block{table("budget", aligns, head, body, foot)}
	if b.Notes != "" {
		blocks = append(blocks, &pandocast.Para{Inlines: pandocast.Text(b.Notes)})
	}
	return blocks, nil
}
