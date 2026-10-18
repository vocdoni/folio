// SPDX-License-Identifier: AGPL-3.0-only

package components

import "github.com/vocdoni/folio/internal/pandocast"

// budgetColumns are the line-item fields in column order with their
// alignment; the label key of each is "budget.<field>".
var budgetColumns = []struct{ key, align string }{
	{"description", "AlignLeft"},
	{"quantity", "AlignRight"},
	{"unit", "AlignLeft"},
	{"unit_price", "AlignRight"},
	{"discount", "AlignRight"},
	{"amount", "AlignRight"},
}

// expandBudget renders the line items (Description and Amount always, the
// other columns only when some line has a value), then the caller's totals
// rows in order (emphasis = bold), then the notes paragraph. Every value is
// shown exactly as sent.
func expandBudget(raw any, ctx Context) []pandocast.Block {
	m, _ := raw.(map[string]any)
	aligns := make([]string, len(budgetColumns))
	head := make([]string, len(budgetColumns))
	for i, c := range budgetColumns {
		aligns[i], head[i] = c.align, ctx.label("budget."+c.key)
	}
	items, _ := m["items"].([]any)
	var body []pandocast.Row
	for _, it := range items {
		im, _ := it.(map[string]any)
		cells := make([]pandocast.Cell, len(budgetColumns))
		for i, c := range budgetColumns {
			cells[i] = cell(plain(str(im, c.key)), "AlignDefault")
		}
		body = append(body, row(cells...))
	}
	aligns, head, body = dropEmptyColumns(aligns, head, body, 0, len(budgetColumns)-1)

	n := len(aligns)
	totals, _ := m["totals"].([]any)
	var foot []pandocast.Row
	for _, it := range totals {
		tm, _ := it.(map[string]any)
		label, amount := plain(str(tm, "label")), plain(str(tm, "amount"))
		if emphasis, _ := tm["emphasis"].(bool); emphasis {
			label, amount = strongPlain(str(tm, "label")), strongPlain(str(tm, "amount"))
		}
		lc := cell(label, "AlignDefault")
		lc.ColSpan = n - 1
		foot = append(foot, row(lc, cell(amount, "AlignDefault")))
	}
	blocks := []pandocast.Block{table("budget", aligns, head, body, foot)}
	if notes := str(m, "notes"); notes != "" {
		blocks = append(blocks, &pandocast.Para{Inlines: inlines(notes)})
	}
	return blocks
}
