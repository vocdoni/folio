// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/money"
	"github.com/vocdoni/folio/internal/pandocast"
)

// Payments is the computed payment schedule (index-aligned with
// data.milestones; entries without payment_pct have an empty Amount).
type Payments struct {
	Currency string    `json:"currency"`
	Items    []Payment `json:"items"`
	cur      money.Currency
}

type Payment struct {
	Pct    string `json:"pct,omitempty"`
	Amount string `json:"amount,omitempty"`
}

func computePayments(raw any, b *BudgetComputed, path string) (*Payments, []model.Finding) {
	list, _ := raw.([]any)
	var fs []model.Finding
	var pcts []*money.Decimal
	var idx []int
	for i, it := range list {
		m, _ := it.(map[string]any)
		if d := parseField(m, "payment_pct", path+"/"+strconv.Itoa(i), 2, true, &fs); d != nil {
			pcts = append(pcts, d)
			idx = append(idx, i)
		}
	}
	if len(fs) > 0 {
		return nil, fs
	}
	if len(pcts) == 0 {
		return nil, []model.Finding{{Code: model.CodePaymentSum, Severity: model.SeverityError, Path: path,
			Message: "show_payments=true but no milestone has payment_pct"}}
	}
	amounts, err := money.SplitPayments(b.totals.Total, pcts, b.in.Currency)
	if err != nil {
		return nil, []model.Finding{{Code: model.CodePaymentSum, Severity: model.SeverityError, Path: path,
			Message: err.Error(), Hint: "payment_pct values must add up to exactly 100"}}
	}
	p := &Payments{Currency: b.Currency, Items: make([]Payment, len(list)), cur: b.in.Currency}
	for k, i := range idx {
		p.Items[i] = Payment{Pct: pcts[k].Text('f'), Amount: amounts[k].Text('f')}
	}
	return p, nil
}

// expandMilestones: Name | When | Description | Deliverables | [Payment].
func expandMilestones(raw any, p *Payments, ctx Context) ([]pandocast.Block, error) {
	list, _ := raw.([]any)
	aligns := []string{"AlignLeft", "AlignLeft", "AlignLeft", "AlignLeft"}
	head := []string{ctx.label("milestones.name"), ctx.label("milestones.date"), ctx.label("milestones.description"), ctx.label("milestones.deliverables")}
	if p != nil {
		aligns, head = append(aligns, "AlignRight"), append(head, ctx.label("milestones.payment"))
	}
	var body []pandocast.Row
	for i, it := range list {
		m, _ := it.(map[string]any)
		when := str(m, "date")
		if d := str(m, "duration"); d != "" {
			when = strings.TrimSpace(when + " " + d)
		}
		var dels []string
		if ds, ok := m["deliverables"].([]any); ok {
			for _, d := range ds {
				if s, ok := d.(string); ok {
					dels = append(dels, s)
				}
			}
		}
		cells := []pandocast.Cell{
			cell(plain(str(m, "name")), "AlignDefault"), cell(plain(when), "AlignDefault"),
			cell(plain(str(m, "description")), "AlignDefault"), cell(plain(strings.Join(dels, ", ")), "AlignDefault"),
		}
		if p != nil {
			text := ""
			if pay := p.Items[i]; pay.Amount != "" {
				pct, _ := money.Parse(pay.Pct, 2)
				a, _ := money.Parse(pay.Amount, 4)
				f, err := money.FormatAmount(a, p.cur, ctx.Lang)
				if err != nil {
					return nil, err
				}
				text = fmt.Sprintf("%s · %s", money.FormatPercent(pct, ctx.Lang), f)
			}
			cells = append(cells, cell(plain(text), "AlignDefault"))
		}
		body = append(body, row(cells...))
	}
	return []pandocast.Block{table("milestones", aligns, head, body, nil)}, nil
}

// expandDeliverables: ID | Name | Description | Due | Acceptance.
func expandDeliverables(raw any, ctx Context) ([]pandocast.Block, error) {
	list, _ := raw.([]any)
	keys := []string{"id", "name", "description", "due", "acceptance"}
	head := make([]string, len(keys))
	aligns := make([]string, len(keys))
	for i, k := range keys {
		head[i], aligns[i] = ctx.label("deliverables."+k), "AlignLeft"
	}
	var body []pandocast.Row
	for _, it := range list {
		m, _ := it.(map[string]any)
		cells := make([]pandocast.Cell, len(keys))
		for i, k := range keys {
			cells[i] = cell(plain(str(m, k)), "AlignDefault")
		}
		body = append(body, row(cells...))
	}
	return []pandocast.Block{table("deliverables", aligns, head, body, nil)}, nil
}

// expandSignatures renders one column per signer (up to 3 per table):
// a signature line, the name (strong), role, organization and a date line.
func expandSignatures(raw any, ctx Context) ([]pandocast.Block, error) {
	list, _ := raw.([]any)
	var blocks []pandocast.Block
	for start := 0; start < len(list); start += 3 {
		end := min(start+3, len(list))
		group := list[start:end]
		aligns := make([]string, len(group))
		for i := range group {
			aligns[i] = "AlignLeft"
		}
		rows := make([][]pandocast.Cell, 4)
		for _, it := range group {
			m, _ := it.(map[string]any)
			rows[0] = append(rows[0], cell(plain("______________________"), "AlignDefault"))
			rows[1] = append(rows[1], cell(strongPlain(str(m, "name")), "AlignDefault"))
			rows[2] = append(rows[2], cell(plain(joinNonEmpty(", ", str(m, "role"), str(m, "organization"))), "AlignDefault"))
			rows[3] = append(rows[3], cell(plain(ctx.label("signatures.date")+": ____________"), "AlignDefault"))
		}
		var body []pandocast.Row
		for _, r := range rows {
			body = append(body, row(r...))
		}
		blocks = append(blocks, table("signatures", aligns, nil, body, nil))
	}
	return blocks, nil
}

// expandInfo renders a two-column key/value table of the metadata fields
// client, project, reference, date, author, version_label (present ones only).
func expandInfo(ctx Context) []pandocast.Block {
	var body []pandocast.Row
	for _, k := range []string{"client", "project", "reference", "date", "author", "version_label"} {
		v, _ := ctx.Metadata[k].(string)
		if strings.TrimSpace(v) == "" {
			continue
		}
		body = append(body, row(cell(strongPlain(ctx.label("info."+k)), "AlignDefault"), cell(plain(v), "AlignDefault")))
	}
	return []pandocast.Block{table("info", []string{"AlignLeft", "AlignLeft"}, nil, body, nil)}
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
