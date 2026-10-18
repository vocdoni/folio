// SPDX-License-Identifier: AGPL-3.0-only

package pandocast

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrTooDeep is returned when the JSON nesting exceeds MaxDecodeDepth.
var ErrTooDeep = errors.New("pandocast: document nested too deeply")

type node struct {
	T string          `json:"t"`
	C json.RawMessage `json:"c,omitempty"`
}

type docJSON struct {
	APIVersion []int                      `json:"pandoc-api-version"`
	Meta       map[string]json.RawMessage `json:"meta"`
	Blocks     []json.RawMessage          `json:"blocks"`
}

// Decode parses pandoc JSON. It rejects other API versions and unknown constructors.
func Decode(data []byte) (*Document, error) {
	var dj docJSON
	if err := json.Unmarshal(data, &dj); err != nil {
		return nil, fmt.Errorf("pandocast: decode document: %w", err)
	}
	if len(dj.APIVersion) != 4 || [4]int(dj.APIVersion) != APIVersion {
		return nil, fmt.Errorf("pandocast: unsupported pandoc-api-version %v (want %v)", dj.APIVersion, APIVersion)
	}
	blocks, err := decodeBlockList(dj.Blocks, 0)
	if err != nil {
		return nil, err
	}
	if dj.Meta == nil {
		dj.Meta = map[string]json.RawMessage{}
	}
	return &Document{Meta: dj.Meta, Blocks: blocks}, nil
}

// Encode writes pandoc JSON (compact, deterministic).
func Encode(doc *Document) ([]byte, error) {
	blocks, err := encodeBlocks(doc.Blocks)
	if err != nil {
		return nil, err
	}
	meta := doc.Meta
	if meta == nil {
		meta = map[string]json.RawMessage{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		APIVersion [4]int                     `json:"pandoc-api-version"`
		Meta       map[string]json.RawMessage `json:"meta"`
		Blocks     []json.RawMessage          `json:"blocks"`
	}{APIVersion, meta, blocks}); err != nil {
		return nil, fmt.Errorf("pandocast: encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ---------- decoding helpers ----------

func tuple(raw json.RawMessage, n int, what string) ([]json.RawMessage, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("pandocast: %s: %w", what, err)
	}
	if len(parts) != n {
		return nil, fmt.Errorf("pandocast: %s: want %d fields, got %d", what, n, len(parts))
	}
	return parts, nil
}

func decodeNode(raw json.RawMessage) (node, error) {
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		return n, fmt.Errorf("pandocast: node: %w", err)
	}
	return n, nil
}

func decodeBlockList(raws []json.RawMessage, depth int) ([]Block, error) {
	if depth > MaxDecodeDepth {
		return nil, ErrTooDeep
	}
	out := make([]Block, 0, len(raws))
	for _, r := range raws {
		b, err := decodeBlock(r, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func decodeBlocks(raw json.RawMessage, depth int) ([]Block, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("pandocast: blocks: %w", err)
	}
	return decodeBlockList(raws, depth)
}

func decodeBlockLists(raw json.RawMessage, depth int) ([][]Block, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("pandocast: block lists: %w", err)
	}
	out := make([][]Block, 0, len(raws))
	for _, r := range raws {
		bs, err := decodeBlocks(r, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, bs)
	}
	return out, nil
}

func decodeInlines(raw json.RawMessage, depth int) ([]Inline, error) {
	if depth > MaxDecodeDepth {
		return nil, ErrTooDeep
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("pandocast: inlines: %w", err)
	}
	out := make([]Inline, 0, len(raws))
	for _, r := range raws {
		in, err := decodeInline(r, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

func decodeAttr(raw json.RawMessage) (Attr, error) {
	var a Attr
	p, err := tuple(raw, 3, "Attr")
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(p[0], &a.ID); err != nil {
		return a, fmt.Errorf("pandocast: attr id: %w", err)
	}
	if err := json.Unmarshal(p[1], &a.Classes); err != nil {
		return a, fmt.Errorf("pandocast: attr classes: %w", err)
	}
	if err := json.Unmarshal(p[2], &a.KeyVals); err != nil {
		return a, fmt.Errorf("pandocast: attr keyvals: %w", err)
	}
	if a.Classes == nil {
		a.Classes = []string{}
	}
	if a.KeyVals == nil {
		a.KeyVals = [][2]string{}
	}
	return a, nil
}

func decodeTarget(raw json.RawMessage) (Target, error) {
	var parts [2]string
	if err := json.Unmarshal(raw, &parts); err != nil {
		return Target{}, fmt.Errorf("pandocast: target: %w", err)
	}
	return Target{URL: parts[0], Title: parts[1]}, nil
}

func decodeString(raw json.RawMessage, what string) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("pandocast: %s: %w", what, err)
	}
	return s, nil
}

func decodeInt(raw json.RawMessage, what string) (int, error) {
	var i int
	if err := json.Unmarshal(raw, &i); err != nil {
		return 0, fmt.Errorf("pandocast: %s: %w", what, err)
	}
	return i, nil
}

// decodeTag reads a nullary constructor {"t":"X"} and returns X.
func decodeTag(raw json.RawMessage, what string) (string, error) {
	n, err := decodeNode(raw)
	if err != nil {
		return "", err
	}
	if n.T == "" || len(n.C) != 0 {
		return "", fmt.Errorf("pandocast: %s: malformed tag", what)
	}
	return n.T, nil
}

func decodeAttrText(c json.RawMessage, what string) (Attr, string, error) {
	p, err := tuple(c, 2, what)
	if err != nil {
		return Attr{}, "", err
	}
	a, err := decodeAttr(p[0])
	if err != nil {
		return Attr{}, "", err
	}
	s, err := decodeString(p[1], what)
	return a, s, err
}

func decodeFormatText(c json.RawMessage, what string) (string, string, error) {
	p, err := tuple(c, 2, what)
	if err != nil {
		return "", "", err
	}
	f, err := decodeString(p[0], what+" format")
	if err != nil {
		return "", "", err
	}
	s, err := decodeString(p[1], what+" text")
	return f, s, err
}

func decodeCaption(raw json.RawMessage, depth int) (Caption, error) {
	var c Caption
	p, err := tuple(raw, 2, "Caption")
	if err != nil {
		return c, err
	}
	if string(p[0]) != "null" {
		if c.Short, err = decodeInlines(p[0], depth+1); err != nil {
			return c, err
		}
	}
	c.Long, err = decodeBlocks(p[1], depth+1)
	return c, err
}

func decodeRows(raw json.RawMessage, depth int) ([]Row, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("pandocast: rows: %w", err)
	}
	rows := make([]Row, 0, len(raws))
	for _, rr := range raws {
		p, err := tuple(rr, 2, "Row")
		if err != nil {
			return nil, err
		}
		attr, err := decodeAttr(p[0])
		if err != nil {
			return nil, err
		}
		var cellRaws []json.RawMessage
		if err := json.Unmarshal(p[1], &cellRaws); err != nil {
			return nil, fmt.Errorf("pandocast: cells: %w", err)
		}
		row := Row{Attr: attr, Cells: make([]Cell, 0, len(cellRaws))}
		for _, cr := range cellRaws {
			cp, err := tuple(cr, 5, "Cell")
			if err != nil {
				return nil, err
			}
			var cell Cell
			if cell.Attr, err = decodeAttr(cp[0]); err != nil {
				return nil, err
			}
			if cell.Align, err = decodeTag(cp[1], "cell alignment"); err != nil {
				return nil, err
			}
			if cell.RowSpan, err = decodeInt(cp[2], "rowspan"); err != nil {
				return nil, err
			}
			if cell.ColSpan, err = decodeInt(cp[3], "colspan"); err != nil {
				return nil, err
			}
			if cell.Blocks, err = decodeBlocks(cp[4], depth+1); err != nil {
				return nil, err
			}
			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func decodeTable(c json.RawMessage, depth int) (*Table, error) {
	p, err := tuple(c, 6, "Table")
	if err != nil {
		return nil, err
	}
	t := &Table{}
	if t.Attr, err = decodeAttr(p[0]); err != nil {
		return nil, err
	}
	if t.Caption, err = decodeCaption(p[1], depth); err != nil {
		return nil, err
	}
	var specs []json.RawMessage
	if err := json.Unmarshal(p[2], &specs); err != nil {
		return nil, fmt.Errorf("pandocast: colspecs: %w", err)
	}
	t.ColSpecs = make([]ColSpec, 0, len(specs))
	for _, s := range specs {
		sp, err := tuple(s, 2, "ColSpec")
		if err != nil {
			return nil, err
		}
		var cs ColSpec
		if cs.Align, err = decodeTag(sp[0], "alignment"); err != nil {
			return nil, err
		}
		wn, err := decodeNode(sp[1])
		if err != nil {
			return nil, err
		}
		switch wn.T {
		case "ColWidthDefault":
		case "ColWidth":
			dec := json.NewDecoder(bytes.NewReader(wn.C))
			dec.UseNumber()
			if err := dec.Decode(&cs.Width); err != nil {
				return nil, fmt.Errorf("pandocast: colwidth: %w", err)
			}
		default:
			return nil, fmt.Errorf("pandocast: unknown column width %q", wn.T)
		}
		t.ColSpecs = append(t.ColSpecs, cs)
	}
	hp, err := tuple(p[3], 2, "TableHead")
	if err != nil {
		return nil, err
	}
	if t.Head.Attr, err = decodeAttr(hp[0]); err != nil {
		return nil, err
	}
	if t.Head.Rows, err = decodeRows(hp[1], depth); err != nil {
		return nil, err
	}
	var bodies []json.RawMessage
	if err := json.Unmarshal(p[4], &bodies); err != nil {
		return nil, fmt.Errorf("pandocast: table bodies: %w", err)
	}
	t.Bodies = make([]TableBody, 0, len(bodies))
	for _, br := range bodies {
		bp, err := tuple(br, 4, "TableBody")
		if err != nil {
			return nil, err
		}
		var tb TableBody
		if tb.Attr, err = decodeAttr(bp[0]); err != nil {
			return nil, err
		}
		if tb.RowHeadColumns, err = decodeInt(bp[1], "row head columns"); err != nil {
			return nil, err
		}
		if tb.Head, err = decodeRows(bp[2], depth); err != nil {
			return nil, err
		}
		if tb.Body, err = decodeRows(bp[3], depth); err != nil {
			return nil, err
		}
		t.Bodies = append(t.Bodies, tb)
	}
	fp, err := tuple(p[5], 2, "TableFoot")
	if err != nil {
		return nil, err
	}
	if t.Foot.Attr, err = decodeAttr(fp[0]); err != nil {
		return nil, err
	}
	if t.Foot.Rows, err = decodeRows(fp[1], depth); err != nil {
		return nil, err
	}
	return t, nil
}

func decodeBlock(raw json.RawMessage, depth int) (Block, error) {
	n, err := decodeNode(raw)
	if err != nil {
		return nil, err
	}
	d := depth + 1
	switch n.T {
	case "Plain", "Para":
		ins, err := decodeInlines(n.C, d)
		if err != nil {
			return nil, err
		}
		if n.T == "Plain" {
			return &Plain{Inlines: ins}, nil
		}
		return &Para{Inlines: ins}, nil
	case "LineBlock":
		var raws []json.RawMessage
		if err := json.Unmarshal(n.C, &raws); err != nil {
			return nil, fmt.Errorf("pandocast: LineBlock: %w", err)
		}
		lb := &LineBlock{Lines: make([][]Inline, 0, len(raws))}
		for _, r := range raws {
			ins, err := decodeInlines(r, d)
			if err != nil {
				return nil, err
			}
			lb.Lines = append(lb.Lines, ins)
		}
		return lb, nil
	case "CodeBlock":
		a, s, err := decodeAttrText(n.C, "CodeBlock")
		if err != nil {
			return nil, err
		}
		return &CodeBlock{Attr: a, Text: s}, nil
	case "RawBlock":
		f, s, err := decodeFormatText(n.C, "RawBlock")
		if err != nil {
			return nil, err
		}
		return &RawBlock{Format: f, Text: s}, nil
	case "BlockQuote":
		bs, err := decodeBlocks(n.C, d)
		if err != nil {
			return nil, err
		}
		return &BlockQuote{Blocks: bs}, nil
	case "OrderedList":
		p, err := tuple(n.C, 2, "OrderedList")
		if err != nil {
			return nil, err
		}
		la, err := tuple(p[0], 3, "ListAttributes")
		if err != nil {
			return nil, err
		}
		ol := &OrderedList{}
		if ol.ListAttrs.Start, err = decodeInt(la[0], "list start"); err != nil {
			return nil, err
		}
		if ol.ListAttrs.Style, err = decodeTag(la[1], "list style"); err != nil {
			return nil, err
		}
		if ol.ListAttrs.Delim, err = decodeTag(la[2], "list delimiter"); err != nil {
			return nil, err
		}
		if ol.Items, err = decodeBlockLists(p[1], d); err != nil {
			return nil, err
		}
		return ol, nil
	case "BulletList":
		items, err := decodeBlockLists(n.C, d)
		if err != nil {
			return nil, err
		}
		return &BulletList{Items: items}, nil
	case "DefinitionList":
		var raws []json.RawMessage
		if err := json.Unmarshal(n.C, &raws); err != nil {
			return nil, fmt.Errorf("pandocast: DefinitionList: %w", err)
		}
		dl := &DefinitionList{Items: make([]DefinitionItem, 0, len(raws))}
		for _, r := range raws {
			p, err := tuple(r, 2, "DefinitionItem")
			if err != nil {
				return nil, err
			}
			var it DefinitionItem
			if it.Term, err = decodeInlines(p[0], d); err != nil {
				return nil, err
			}
			if it.Defs, err = decodeBlockLists(p[1], d); err != nil {
				return nil, err
			}
			dl.Items = append(dl.Items, it)
		}
		return dl, nil
	case "Header":
		p, err := tuple(n.C, 3, "Header")
		if err != nil {
			return nil, err
		}
		h := &Header{}
		if h.Level, err = decodeInt(p[0], "header level"); err != nil {
			return nil, err
		}
		if h.Attr, err = decodeAttr(p[1]); err != nil {
			return nil, err
		}
		if h.Inlines, err = decodeInlines(p[2], d); err != nil {
			return nil, err
		}
		return h, nil
	case "HorizontalRule":
		return &HorizontalRule{}, nil
	case "Table":
		return decodeTable(n.C, d)
	case "Figure":
		p, err := tuple(n.C, 3, "Figure")
		if err != nil {
			return nil, err
		}
		f := &Figure{}
		if f.Attr, err = decodeAttr(p[0]); err != nil {
			return nil, err
		}
		if f.Caption, err = decodeCaption(p[1], d); err != nil {
			return nil, err
		}
		if f.Blocks, err = decodeBlocks(p[2], d); err != nil {
			return nil, err
		}
		return f, nil
	case "Div":
		p, err := tuple(n.C, 2, "Div")
		if err != nil {
			return nil, err
		}
		dv := &Div{}
		if dv.Attr, err = decodeAttr(p[0]); err != nil {
			return nil, err
		}
		if dv.Blocks, err = decodeBlocks(p[1], d); err != nil {
			return nil, err
		}
		return dv, nil
	default:
		return nil, fmt.Errorf("pandocast: unknown block %q", n.T)
	}
}

func decodeInline(raw json.RawMessage, depth int) (Inline, error) {
	n, err := decodeNode(raw)
	if err != nil {
		return nil, err
	}
	d := depth + 1
	switch n.T {
	case "Str":
		s, err := decodeString(n.C, "Str")
		if err != nil {
			return nil, err
		}
		return &Str{Text: s}, nil
	case "Emph", "Underline", "Strong", "Strikeout", "Superscript", "Subscript", "SmallCaps":
		ins, err := decodeInlines(n.C, d)
		if err != nil {
			return nil, err
		}
		switch n.T {
		case "Emph":
			return &Emph{Inlines: ins}, nil
		case "Underline":
			return &Underline{Inlines: ins}, nil
		case "Strong":
			return &Strong{Inlines: ins}, nil
		case "Strikeout":
			return &Strikeout{Inlines: ins}, nil
		case "Superscript":
			return &Superscript{Inlines: ins}, nil
		case "Subscript":
			return &Subscript{Inlines: ins}, nil
		default:
			return &SmallCaps{Inlines: ins}, nil
		}
	case "Quoted":
		p, err := tuple(n.C, 2, "Quoted")
		if err != nil {
			return nil, err
		}
		q := &Quoted{}
		if q.QuoteType, err = decodeTag(p[0], "quote type"); err != nil {
			return nil, err
		}
		if q.Inlines, err = decodeInlines(p[1], d); err != nil {
			return nil, err
		}
		return q, nil
	case "Cite":
		p, err := tuple(n.C, 2, "Cite")
		if err != nil {
			return nil, err
		}
		c := &Cite{}
		if err := json.Unmarshal(p[0], &c.Citations); err != nil {
			return nil, fmt.Errorf("pandocast: citations: %w", err)
		}
		if c.Inlines, err = decodeInlines(p[1], d); err != nil {
			return nil, err
		}
		return c, nil
	case "Code":
		a, s, err := decodeAttrText(n.C, "Code")
		if err != nil {
			return nil, err
		}
		return &Code{Attr: a, Text: s}, nil
	case "Space":
		return &Space{}, nil
	case "SoftBreak":
		return &SoftBreak{}, nil
	case "LineBreak":
		return &LineBreak{}, nil
	case "Math":
		p, err := tuple(n.C, 2, "Math")
		if err != nil {
			return nil, err
		}
		m := &Math{}
		if m.MathType, err = decodeTag(p[0], "math type"); err != nil {
			return nil, err
		}
		if m.Text, err = decodeString(p[1], "math"); err != nil {
			return nil, err
		}
		return m, nil
	case "RawInline":
		f, s, err := decodeFormatText(n.C, "RawInline")
		if err != nil {
			return nil, err
		}
		return &RawInline{Format: f, Text: s}, nil
	case "Link", "Image":
		p, err := tuple(n.C, 3, n.T)
		if err != nil {
			return nil, err
		}
		a, err := decodeAttr(p[0])
		if err != nil {
			return nil, err
		}
		ins, err := decodeInlines(p[1], d)
		if err != nil {
			return nil, err
		}
		tg, err := decodeTarget(p[2])
		if err != nil {
			return nil, err
		}
		if n.T == "Link" {
			return &Link{Attr: a, Inlines: ins, Target: tg}, nil
		}
		return &Image{Attr: a, Inlines: ins, Target: tg}, nil
	case "Note":
		bs, err := decodeBlocks(n.C, d)
		if err != nil {
			return nil, err
		}
		return &Note{Blocks: bs}, nil
	case "Span":
		p, err := tuple(n.C, 2, "Span")
		if err != nil {
			return nil, err
		}
		s := &Span{}
		if s.Attr, err = decodeAttr(p[0]); err != nil {
			return nil, err
		}
		if s.Inlines, err = decodeInlines(p[1], d); err != nil {
			return nil, err
		}
		return s, nil
	default:
		return nil, fmt.Errorf("pandocast: unknown inline %q", n.T)
	}
}

// MetaString returns the JSON of a pandoc MetaString value (for doc.Meta).
func MetaString(s string) json.RawMessage {
	c, err := marshal(s)
	if err != nil { // a Go string always marshals
		return json.RawMessage(`{"t":"MetaString","c":""}`)
	}
	out, _ := marshal(node{T: "MetaString", C: c})
	return out
}
