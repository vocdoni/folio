// SPDX-License-Identifier: AGPL-3.0-only

package pandocast

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("pandocast: encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func mk(t string, c any) (json.RawMessage, error) {
	if c == nil {
		return marshal(node{T: t})
	}
	cr, err := marshal(c)
	if err != nil {
		return nil, err
	}
	return marshal(node{T: t, C: cr})
}

func tag(t string) json.RawMessage { return json.RawMessage(`{"t":"` + t + `"}`) }

func encAttr(a Attr) []any {
	classes := a.Classes
	if classes == nil {
		classes = []string{}
	}
	kv := a.KeyVals
	if kv == nil {
		kv = [][2]string{}
	}
	return []any{a.ID, classes, kv}
}

func encodeBlocks(bs []Block) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(bs))
	for _, b := range bs {
		r, err := encodeBlock(b)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func encodeBlockLists(ls [][]Block) ([][]json.RawMessage, error) {
	out := make([][]json.RawMessage, 0, len(ls))
	for _, l := range ls {
		r, err := encodeBlocks(l)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func encodeInlines(ins []Inline) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(ins))
	for _, in := range ins {
		r, err := encodeInline(in)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func encCaption(c Caption) ([]any, error) {
	var short any // nil -> null
	if c.Short != nil {
		s, err := encodeInlines(c.Short)
		if err != nil {
			return nil, err
		}
		short = s
	}
	long, err := encodeBlocks(c.Long)
	if err != nil {
		return nil, err
	}
	return []any{short, long}, nil
}

func encRows(rows []Row) ([]any, error) {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		cells := make([]any, 0, len(r.Cells))
		for _, c := range r.Cells {
			bs, err := encodeBlocks(c.Blocks)
			if err != nil {
				return nil, err
			}
			cells = append(cells, []any{encAttr(c.Attr), tag(c.Align), c.RowSpan, c.ColSpan, bs})
		}
		out = append(out, []any{encAttr(r.Attr), cells})
	}
	return out, nil
}

func encTable(t *Table) (json.RawMessage, error) {
	capt, err := encCaption(t.Caption)
	if err != nil {
		return nil, err
	}
	specs := make([]any, 0, len(t.ColSpecs))
	for _, cs := range t.ColSpecs {
		var w json.RawMessage
		if cs.Width == "" {
			w = tag("ColWidthDefault")
		} else {
			w = json.RawMessage(`{"t":"ColWidth","c":` + string(cs.Width) + `}`)
		}
		specs = append(specs, []any{tag(cs.Align), w})
	}
	headRows, err := encRows(t.Head.Rows)
	if err != nil {
		return nil, err
	}
	bodies := make([]any, 0, len(t.Bodies))
	for _, b := range t.Bodies {
		h, err := encRows(b.Head)
		if err != nil {
			return nil, err
		}
		r, err := encRows(b.Body)
		if err != nil {
			return nil, err
		}
		bodies = append(bodies, []any{encAttr(b.Attr), b.RowHeadColumns, h, r})
	}
	footRows, err := encRows(t.Foot.Rows)
	if err != nil {
		return nil, err
	}
	return mk("Table", []any{encAttr(t.Attr), capt, specs,
		[]any{encAttr(t.Head.Attr), headRows}, bodies, []any{encAttr(t.Foot.Attr), footRows}})
}

func encodeBlock(b Block) (json.RawMessage, error) {
	switch v := b.(type) {
	case *Plain:
		ins, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Plain", ins)
	case *Para:
		ins, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Para", ins)
	case *LineBlock:
		lines := make([]any, 0, len(v.Lines))
		for _, l := range v.Lines {
			ins, err := encodeInlines(l)
			if err != nil {
				return nil, err
			}
			lines = append(lines, ins)
		}
		return mk("LineBlock", lines)
	case *CodeBlock:
		return mk("CodeBlock", []any{encAttr(v.Attr), v.Text})
	case *RawBlock:
		return mk("RawBlock", []any{v.Format, v.Text})
	case *BlockQuote:
		bs, err := encodeBlocks(v.Blocks)
		if err != nil {
			return nil, err
		}
		return mk("BlockQuote", bs)
	case *OrderedList:
		items, err := encodeBlockLists(v.Items)
		if err != nil {
			return nil, err
		}
		la := []any{v.ListAttrs.Start, tag(v.ListAttrs.Style), tag(v.ListAttrs.Delim)}
		return mk("OrderedList", []any{la, items})
	case *BulletList:
		items, err := encodeBlockLists(v.Items)
		if err != nil {
			return nil, err
		}
		return mk("BulletList", items)
	case *DefinitionList:
		items := make([]any, 0, len(v.Items))
		for _, it := range v.Items {
			term, err := encodeInlines(it.Term)
			if err != nil {
				return nil, err
			}
			defs, err := encodeBlockLists(it.Defs)
			if err != nil {
				return nil, err
			}
			items = append(items, []any{term, defs})
		}
		return mk("DefinitionList", items)
	case *Header:
		ins, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Header", []any{v.Level, encAttr(v.Attr), ins})
	case *HorizontalRule:
		return mk("HorizontalRule", nil)
	case *Table:
		return encTable(v)
	case *Figure:
		capt, err := encCaption(v.Caption)
		if err != nil {
			return nil, err
		}
		bs, err := encodeBlocks(v.Blocks)
		if err != nil {
			return nil, err
		}
		return mk("Figure", []any{encAttr(v.Attr), capt, bs})
	case *Div:
		bs, err := encodeBlocks(v.Blocks)
		if err != nil {
			return nil, err
		}
		return mk("Div", []any{encAttr(v.Attr), bs})
	default:
		return nil, fmt.Errorf("pandocast: cannot encode block %T", b)
	}
}

func encodeInline(in Inline) (json.RawMessage, error) {
	wrap := func(t string, ins []Inline) (json.RawMessage, error) {
		r, err := encodeInlines(ins)
		if err != nil {
			return nil, err
		}
		return mk(t, r)
	}
	switch v := in.(type) {
	case *Str:
		return mk("Str", v.Text)
	case *Emph:
		return wrap("Emph", v.Inlines)
	case *Underline:
		return wrap("Underline", v.Inlines)
	case *Strong:
		return wrap("Strong", v.Inlines)
	case *Strikeout:
		return wrap("Strikeout", v.Inlines)
	case *Superscript:
		return wrap("Superscript", v.Inlines)
	case *Subscript:
		return wrap("Subscript", v.Inlines)
	case *SmallCaps:
		return wrap("SmallCaps", v.Inlines)
	case *Quoted:
		r, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Quoted", []any{tag(v.QuoteType), r})
	case *Cite:
		r, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		cits := v.Citations
		if cits == nil {
			cits = []json.RawMessage{}
		}
		return mk("Cite", []any{cits, r})
	case *Code:
		return mk("Code", []any{encAttr(v.Attr), v.Text})
	case *Space:
		return mk("Space", nil)
	case *SoftBreak:
		return mk("SoftBreak", nil)
	case *LineBreak:
		return mk("LineBreak", nil)
	case *Math:
		return mk("Math", []any{tag(v.MathType), v.Text})
	case *RawInline:
		return mk("RawInline", []any{v.Format, v.Text})
	case *Link:
		r, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Link", []any{encAttr(v.Attr), r, []string{v.Target.URL, v.Target.Title}})
	case *Image:
		r, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Image", []any{encAttr(v.Attr), r, []string{v.Target.URL, v.Target.Title}})
	case *Note:
		bs, err := encodeBlocks(v.Blocks)
		if err != nil {
			return nil, err
		}
		return mk("Note", bs)
	case *Span:
		r, err := encodeInlines(v.Inlines)
		if err != nil {
			return nil, err
		}
		return mk("Span", []any{encAttr(v.Attr), r})
	default:
		return nil, fmt.Errorf("pandocast: cannot encode inline %T", in)
	}
}
