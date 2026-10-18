// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
)

// Check runs the cross-component checks the JSON schemas cannot express: when
// the deliverables component is used, every id listed in
// data.milestones[].deliverables must exist in data.deliverables[].id.
// Findings carry JSON pointers; malformed data (already reported by the
// schemas) is skipped.
func Check(data map[string]any, uses []Use) []model.Finding {
	usesDeliverables := false
	for _, u := range uses {
		usesDeliverables = usesDeliverables || u.Name == "deliverables"
	}
	if !usesDeliverables {
		return nil
	}
	var ids []string
	known := map[string]bool{}
	dl, _ := data["deliverables"].([]any)
	for _, it := range dl {
		m, _ := it.(map[string]any)
		if id, ok := m["id"].(string); ok {
			ids = append(ids, id)
			known[id] = true
		}
	}
	var fs []model.Finding
	ms, _ := data["milestones"].([]any)
	for i, it := range ms {
		m, _ := it.(map[string]any)
		refs, _ := m["deliverables"].([]any)
		for j, r := range refs {
			id, ok := r.(string)
			if !ok || known[id] {
				continue
			}
			hint := "add it to data.deliverables or remove it from the milestone"
			if len(ids) > 0 {
				hint = "use one of the ids in data.deliverables: " + strings.Join(ids, ", ")
			}
			fs = append(fs, model.Finding{Code: model.CodeUnknownDeliverable, Severity: model.SeverityError,
				Path:    "/data/milestones/" + strconv.Itoa(i) + "/deliverables/" + strconv.Itoa(j),
				Message: fmt.Sprintf("milestone %q refers to deliverable %q, which is not in data.deliverables", str(m, "name"), id),
				Hint:    hint})
		}
	}
	return fs
}

// expandMilestones: Name | When | Description | Deliverables | Payment; the
// last three only when some milestone has them. Payment is shown verbatim.
func expandMilestones(raw any, ctx Context) []pandocast.Block {
	list, _ := raw.([]any)
	aligns := []string{"AlignLeft", "AlignLeft", "AlignLeft", "AlignLeft", "AlignRight"}
	head := []string{ctx.label("milestones.name"), ctx.label("milestones.date"), ctx.label("milestones.description"),
		ctx.label("milestones.deliverables"), ctx.label("milestones.payment")}
	var body []pandocast.Row
	for _, it := range list {
		m, _ := it.(map[string]any)
		when := str(m, "date")
		when = joinNonEmpty(" ", when, str(m, "duration"))
		var dels []string
		if ds, ok := m["deliverables"].([]any); ok {
			for _, d := range ds {
				if s, ok := d.(string); ok {
					dels = append(dels, s)
				}
			}
		}
		body = append(body, row(
			cell(plain(str(m, "name")), "AlignDefault"), cell(plain(when), "AlignDefault"),
			cell(plain(str(m, "description")), "AlignDefault"), cell(plain(strings.Join(dels, ", ")), "AlignDefault"),
			cell(plain(str(m, "payment")), "AlignDefault")))
	}
	// Name and When always stay; empty Description/Deliverables/Payment go.
	aligns, head, body = dropEmptyColumns(aligns, head, body, 0, 1)
	return []pandocast.Block{table("milestones", aligns, head, body, nil)}
}

// expandDeliverables: ID | Name | Description | Due | Acceptance.
func expandDeliverables(raw any, ctx Context) []pandocast.Block {
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
	// ID and Name always stay.
	aligns, head, body = dropEmptyColumns(aligns, head, body, 0, 1)
	return []pandocast.Block{table("deliverables", aligns, head, body, nil)}
}

// expandSignatures renders one column per signer (up to 3 per table):
// a signature line, the name (strong), role, organization and a date line.
func expandSignatures(raw any, ctx Context) []pandocast.Block {
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
	return blocks
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
