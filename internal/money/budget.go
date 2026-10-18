// SPDX-License-Identifier: AGPL-3.0-only

package money

import (
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
)

// Item is one validated budget line. DiscountPct and TaxRate may be nil.
type Item struct {
	Quantity    *Decimal
	UnitPrice   *Decimal
	DiscountPct *Decimal
	TaxRate     *Decimal
}

// Budget is a validated budget. TaxRate and DiscountPct may be nil.
type Budget struct {
	Currency    Currency
	TaxRate     *Decimal
	DiscountPct *Decimal
	Items       []Item
}

// TaxGroup is the tax for all lines sharing one rate.
type TaxGroup struct {
	Rate *Decimal // reduced (e.g. 21, 10.5)
	Base *Decimal // sum of discounted line amounts in the group
	Tax  *Decimal // round(Base × Rate / 100)
}

// Totals is the result of ComputeBudget. All amounts are rounded to the
// currency's minor unit.
type Totals struct {
	LineNets           []*Decimal // per item, before the document discount
	LineDiscounted     []*Decimal // per item, after the document discount
	Subtotal           *Decimal   // Σ LineNets
	DiscountAmount     *Decimal   // Subtotal − DiscountedSubtotal
	DiscountedSubtotal *Decimal   // Σ LineDiscounted
	TaxGroups          []TaxGroup // ascending by rate; includes rate 0 when present
	TaxTotal           *Decimal
	Total              *Decimal // DiscountedSubtotal + TaxTotal
}

// ComputeBudget applies the documented algorithm:
//
//	net_i        = round(qty_i × price_i × (1 − lineDiscount_i/100))
//	subtotal     = Σ net_i
//	discounted_i = round(net_i × (1 − docDiscount/100))
//	tax_g        = round(Σ_{i∈g} discounted_i × rate_g/100), rate_i = item rate ?? budget rate ?? 0
//	total        = Σ discounted_i + Σ tax_g
//
// Rounding is half-up to Currency.MinorUnits. Inputs must already be validated
// (non-negative, percentage ≤ 100, items non-empty).
func ComputeBudget(b Budget) (_ Totals, err error) {
	defer recoverArith(&err)
	if len(b.Items) == 0 {
		return Totals{}, fmt.Errorf("%w: items must not be empty", ErrInvalid)
	}
	m := b.Currency.MinorUnits
	t := Totals{Subtotal: apd.New(0, 0), DiscountedSubtotal: apd.New(0, 0), TaxTotal: apd.New(0, 0)}
	docFactor := apd.New(1, 0)
	if b.DiscountPct != nil {
		docFactor = factor(b.DiscountPct)
	}
	groups := map[string]*TaxGroup{}
	for _, it := range b.Items {
		gross := mul(it.Quantity, it.UnitPrice)
		if it.DiscountPct != nil {
			gross = mul(gross, factor(it.DiscountPct))
		}
		net := round(gross, m)
		disc := round(mul(net, docFactor), m)
		t.LineNets = append(t.LineNets, net)
		t.LineDiscounted = append(t.LineDiscounted, disc)
		t.Subtotal = add(t.Subtotal, net)
		t.DiscountedSubtotal = add(t.DiscountedSubtotal, disc)

		rate := zero
		if it.TaxRate != nil {
			rate = it.TaxRate
		} else if b.TaxRate != nil {
			rate = b.TaxRate
		}
		var key apd.Decimal
		key.Reduce(rate)
		g, ok := groups[key.String()]
		if !ok {
			r := key
			g = &TaxGroup{Rate: &r, Base: apd.New(0, 0)}
			groups[key.String()] = g
		}
		g.Base = add(g.Base, disc)
	}
	for _, g := range groups {
		g.Tax = round(pctOf(g.Base, g.Rate), m)
		t.TaxGroups = append(t.TaxGroups, *g)
		t.TaxTotal = add(t.TaxTotal, g.Tax)
	}
	sort.Slice(t.TaxGroups, func(i, j int) bool { return t.TaxGroups[i].Rate.Cmp(t.TaxGroups[j].Rate) < 0 })
	t.DiscountAmount = sub(t.Subtotal, t.DiscountedSubtotal)
	t.Total = add(t.DiscountedSubtotal, t.TaxTotal)
	return t, nil
}

// SplitPayments splits total by percentages that must sum to exactly 100.
// Every payment but the last is round(total × pct/100); the last absorbs the
// rounding remainder so the payments always sum to total.
func SplitPayments(total *Decimal, pcts []*Decimal, cur Currency) (_ []*Decimal, err error) {
	defer recoverArith(&err)
	if len(pcts) == 0 {
		return nil, nil
	}
	sum := apd.New(0, 0)
	for _, p := range pcts {
		sum = add(sum, p)
	}
	if sum.Cmp(hundred) != 0 {
		return nil, fmt.Errorf("%w: payment percentages sum to %s, must be 100", ErrInvalid, sum.Text('f'))
	}
	out := make([]*Decimal, len(pcts))
	acc := apd.New(0, 0)
	for i, p := range pcts {
		if i == len(pcts)-1 {
			out[i] = round(sub(total, acc), cur.MinorUnits)
			break
		}
		out[i] = round(pctOf(total, p), cur.MinorUnits)
		acc = add(acc, out[i])
	}
	return out, nil
}

// Equal reports a == b numerically ("1210" equals "1210.00").
func Equal(a, b *Decimal) bool { return a.Cmp(b) == 0 }
