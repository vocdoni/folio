// SPDX-License-Identifier: AGPL-3.0-only

// Package components validates component data, computes derived values
// (money via internal/money) and expands component divs into pandoc AST
// blocks. Data values only ever become Str nodes, so they cannot inject markup.
package components

import (
	"embed"
	"fmt"
	"strings"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// Names lists the built-in components in documentation order.
var Names = []string{"budget", "milestones", "deliverables", "signatures", "info"}

// Known reports whether name is a built-in component.
func Known(name string) bool {
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

// Schema returns the JSON Schema (2020-12) for data.<name>; it is also
// served as "folio:component/<name>" for template data_schema $refs.
func Schema(name string) ([]byte, bool) {
	if !Known(name) {
		return nil, false
	}
	b, err := schemaFS.ReadFile("schemas/" + name + ".json")
	return b, err == nil
}

// Use is one component div found in the document.
type Use struct {
	Name  string
	Attrs map[string]string // div key-value attributes, e.g. show_payments=true
	Index int               // index of the div among top-level blocks
}

// Context carries what expansion needs besides data.
type Context struct {
	Lang     string
	Labels   map[string]string // resolved label table (brandkit.ResolveLabels)
	Metadata map[string]any    // effective metadata (decoded with json.Number)
}

func (c Context) label(key string) string {
	if v, ok := c.Labels[key]; ok {
		return v
	}
	return key
}

// Computed is recorded on the revision as `computed`.
type Computed struct {
	Budget     *BudgetComputed `json:"budget,omitempty"`
	Milestones *Payments       `json:"milestones,omitempty"`
}

// Compute validates and computes every used component. data is the decoded
// `data` object (numbers as json.Number). Findings carry JSON pointers.
func Compute(data map[string]any, uses []Use, ctx Context) (Computed, []model.Finding) {
	var out Computed
	var fs []model.Finding
	used := map[string]Use{}
	for _, u := range uses {
		used[u.Name] = u
	}
	if _, ok := used["budget"]; ok {
		b, f := computeBudget(data["budget"], "/data/budget")
		fs = append(fs, f...)
		out.Budget = b
	}
	if u, ok := used["milestones"]; ok && u.Attrs["show_payments"] == "true" {
		if out.Budget == nil {
			fs = append(fs, model.Finding{Code: model.CodeMoneyInvalid, Severity: model.SeverityError,
				Path: "/data/milestones", Message: "show_payments=true needs a budget component in the same document"})
		} else {
			p, f := computePayments(data["milestones"], out.Budget, "/data/milestones")
			fs = append(fs, f...)
			out.Milestones = p
		}
	}
	return out, fs
}

// Expand returns the blocks replacing one component div.
func Expand(u Use, data map[string]any, c Computed, ctx Context) ([]pandocast.Block, error) {
	switch u.Name {
	case "budget":
		if c.Budget == nil {
			return nil, fmt.Errorf("components: budget not computed")
		}
		return expandBudget(c.Budget, ctx)
	case "milestones":
		return expandMilestones(data["milestones"], c.Milestones, ctx)
	case "deliverables":
		return expandDeliverables(data["deliverables"], ctx)
	case "signatures":
		return expandSignatures(data["signatures"], ctx)
	case "info":
		return expandInfo(ctx), nil
	}
	return nil, fmt.Errorf("components: unknown component %q", u.Name)
}

// ---- AST helpers (all text goes through pandocast.Text → Str/Space) ----

func plain(s string) []pandocast.Block {
	if s == "" {
		return []pandocast.Block{}
	}
	return []pandocast.Block{&pandocast.Plain{Inlines: pandocast.Text(s)}}
}

func strongPlain(s string) []pandocast.Block {
	return []pandocast.Block{&pandocast.Plain{Inlines: []pandocast.Inline{&pandocast.Strong{Inlines: pandocast.Text(s)}}}}
}

func cell(blocks []pandocast.Block, align string) pandocast.Cell {
	return pandocast.Cell{Attr: pandocast.NewAttr(""), Align: align, RowSpan: 1, ColSpan: 1, Blocks: blocks}
}

func row(cells ...pandocast.Cell) pandocast.Row {
	return pandocast.Row{Attr: pandocast.NewAttr(""), Cells: cells}
}

// table builds a simple table; aligns has one entry per column
// ("AlignLeft", "AlignRight", ...). A nil head means no header row; foot
// rows are optional.
func table(class string, aligns []string, head []string, body []pandocast.Row, foot []pandocast.Row) *pandocast.Table {
	specs := make([]pandocast.ColSpec, len(aligns))
	headRows := []pandocast.Row{}
	if head != nil {
		headCells := make([]pandocast.Cell, len(aligns))
		for i := range aligns {
			headCells[i] = cell(plain(head[i]), "AlignDefault")
		}
		headRows = append(headRows, row(headCells...))
	}
	for i, a := range aligns {
		specs[i] = pandocast.ColSpec{Align: a}
	}
	if foot == nil {
		foot = []pandocast.Row{}
	}
	return &pandocast.Table{
		Attr:     pandocast.NewAttr("", "folio-"+class),
		Caption:  pandocast.Caption{Long: []pandocast.Block{}},
		ColSpecs: specs,
		Head:     pandocast.TableHead{Attr: pandocast.NewAttr(""), Rows: headRows},
		Bodies:   []pandocast.TableBody{{Attr: pandocast.NewAttr(""), Head: []pandocast.Row{}, Body: body}},
		Foot:     pandocast.TableFoot{Attr: pandocast.NewAttr(""), Rows: foot},
	}
}

func str(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
