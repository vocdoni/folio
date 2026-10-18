// SPDX-License-Identifier: AGPL-3.0-only

// Package components checks component data and expands component divs into
// pandoc AST blocks. Values are rendered verbatim: Folio never parses,
// computes, rounds or reformats them. Data values only ever become Str and
// Space nodes, so they cannot inject markup.
package components

import (
	"embed"
	"fmt"
	"strings"

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
	Index int // index of the div among top-level blocks
}

// Context carries what expansion needs besides data.
type Context struct {
	Labels   map[string]string // resolved label table (brandkit.ResolveLabels)
	Metadata map[string]any    // effective metadata (decoded with json.Number)
}

func (c Context) label(key string) string {
	if v, ok := c.Labels[key]; ok {
		return v
	}
	return key
}

// Expand returns the blocks replacing one component div. data is the
// validated `data` object.
func Expand(u Use, data map[string]any, ctx Context) ([]pandocast.Block, error) {
	switch u.Name {
	case "budget":
		return expandBudget(data["budget"], ctx), nil
	case "milestones":
		return expandMilestones(data["milestones"], ctx), nil
	case "deliverables":
		return expandDeliverables(data["deliverables"], ctx), nil
	case "signatures":
		return expandSignatures(data["signatures"], ctx), nil
	case "info":
		return expandInfo(ctx), nil
	}
	return nil, fmt.Errorf("components: unknown component %q", u.Name)
}

// ---- AST helpers (all text goes through inlines → Str/Space) ----

// isASCIISpace reports the separators that become Space nodes. Unicode
// spaces (no-break, narrow no-break, thin) stay inside the text as sent.
func isASCIISpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}

// inlines splits s into Str and Space nodes on ASCII white space only.
func inlines(s string) []pandocast.Inline {
	out := []pandocast.Inline{}
	for i, w := range strings.FieldsFunc(s, isASCIISpace) {
		if i > 0 {
			out = append(out, &pandocast.Space{})
		}
		out = append(out, &pandocast.Str{Text: w})
	}
	return out
}

func plain(s string) []pandocast.Block {
	if s == "" {
		return []pandocast.Block{}
	}
	return []pandocast.Block{&pandocast.Plain{Inlines: inlines(s)}}
}

func strongPlain(s string) []pandocast.Block {
	return []pandocast.Block{&pandocast.Plain{Inlines: []pandocast.Inline{&pandocast.Strong{Inlines: inlines(s)}}}}
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

// dropEmptyColumns removes every column (from aligns, head and body rows)
// whose body cells are all empty, except the indexes listed in keep. Rows are
// rebuilt so the inputs are not mutated.
func dropEmptyColumns(aligns, head []string, body []pandocast.Row, keep ...int) ([]string, []string, []pandocast.Row) {
	used := make([]bool, len(aligns))
	for _, k := range keep {
		used[k] = true
	}
	for _, r := range body {
		for i, c := range r.Cells {
			if len(c.Blocks) > 0 {
				used[i] = true
			}
		}
	}
	var a, h []string
	for i := range aligns {
		if used[i] {
			a, h = append(a, aligns[i]), append(h, head[i])
		}
	}
	rows := make([]pandocast.Row, len(body))
	for ri, r := range body {
		nr := r
		nr.Cells = nil
		for i, c := range r.Cells {
			if used[i] {
				nr.Cells = append(nr.Cells, c)
			}
		}
		rows[ri] = nr
	}
	return a, h, rows
}

// str returns m[k] when it is a string, without leading or trailing ASCII
// white space (pandoc would drop it anyway); anything else is "".
func str(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return strings.TrimFunc(s, isASCIISpace)
	}
	return ""
}
