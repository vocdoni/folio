// SPDX-License-Identifier: AGPL-3.0-only

package pandocast

import "strings"

// Visitor receives every node in document order. Depth counts enclosing block
// containers (block quotes, list items, definition lists, divs, figures,
// table cells, notes); top-level blocks have depth 0. Returning false from
// Block or Inline skips that node's children. Nil callbacks are ignored.
type Visitor struct {
	Block  func(b Block, depth int) bool
	Inline func(in Inline, depth int) bool
}

// Walk traverses blocks in pre-order. Nodes are pointers, so callbacks may
// mutate them in place (e.g. rewrite Image.Target).
func Walk(blocks []Block, v Visitor) { walkBlocks(blocks, 0, v) }

func walkBlocks(bs []Block, depth int, v Visitor) {
	for _, b := range bs {
		walkBlock(b, depth, v)
	}
}

func walkInlines(ins []Inline, depth int, v Visitor) {
	for _, in := range ins {
		walkInline(in, depth, v)
	}
}

func walkCaption(c Caption, depth int, v Visitor) {
	walkInlines(c.Short, depth, v)
	walkBlocks(c.Long, depth+1, v)
}

func walkRows(rows []Row, depth int, v Visitor) {
	for _, r := range rows {
		for _, c := range r.Cells {
			walkBlocks(c.Blocks, depth+1, v)
		}
	}
}

func walkBlock(b Block, depth int, v Visitor) {
	if v.Block != nil && !v.Block(b, depth) {
		return
	}
	switch x := b.(type) {
	case *Plain:
		walkInlines(x.Inlines, depth, v)
	case *Para:
		walkInlines(x.Inlines, depth, v)
	case *LineBlock:
		for _, l := range x.Lines {
			walkInlines(l, depth, v)
		}
	case *BlockQuote:
		walkBlocks(x.Blocks, depth+1, v)
	case *OrderedList:
		for _, it := range x.Items {
			walkBlocks(it, depth+1, v)
		}
	case *BulletList:
		for _, it := range x.Items {
			walkBlocks(it, depth+1, v)
		}
	case *DefinitionList:
		for _, it := range x.Items {
			walkInlines(it.Term, depth, v)
			for _, d := range it.Defs {
				walkBlocks(d, depth+1, v)
			}
		}
	case *Header:
		walkInlines(x.Inlines, depth, v)
	case *Table:
		walkCaption(x.Caption, depth, v)
		walkRows(x.Head.Rows, depth, v)
		for _, tb := range x.Bodies {
			walkRows(tb.Head, depth, v)
			walkRows(tb.Body, depth, v)
		}
		walkRows(x.Foot.Rows, depth, v)
	case *Figure:
		walkCaption(x.Caption, depth, v)
		walkBlocks(x.Blocks, depth+1, v)
	case *Div:
		walkBlocks(x.Blocks, depth+1, v)
	case *CodeBlock, *RawBlock, *HorizontalRule:
	}
}

func walkInline(in Inline, depth int, v Visitor) {
	if v.Inline != nil && !v.Inline(in, depth) {
		return
	}
	switch x := in.(type) {
	case *Emph:
		walkInlines(x.Inlines, depth, v)
	case *Underline:
		walkInlines(x.Inlines, depth, v)
	case *Strong:
		walkInlines(x.Inlines, depth, v)
	case *Strikeout:
		walkInlines(x.Inlines, depth, v)
	case *Superscript:
		walkInlines(x.Inlines, depth, v)
	case *Subscript:
		walkInlines(x.Inlines, depth, v)
	case *SmallCaps:
		walkInlines(x.Inlines, depth, v)
	case *Quoted:
		walkInlines(x.Inlines, depth, v)
	case *Cite:
		walkInlines(x.Inlines, depth, v)
	case *Link:
		walkInlines(x.Inlines, depth, v)
	case *Image:
		walkInlines(x.Inlines, depth, v)
	case *Span:
		walkInlines(x.Inlines, depth, v)
	case *Note:
		walkBlocks(x.Blocks, depth+1, v)
	}
}

// Stringify returns the plain text of inlines: Str text, spaces and breaks as
// " ", Code and Math text, quotes for Quoted. Notes are skipped.
func Stringify(ins []Inline) string {
	var sb strings.Builder
	var rec func([]Inline)
	rec = func(ins []Inline) {
		for _, in := range ins {
			switch x := in.(type) {
			case *Str:
				sb.WriteString(x.Text)
			case *Space, *SoftBreak, *LineBreak:
				sb.WriteByte(' ')
			case *Code:
				sb.WriteString(x.Text)
			case *Math:
				sb.WriteString(x.Text)
			case *Quoted:
				q := "“"
				e := "”"
				if x.QuoteType == "SingleQuote" {
					q, e = "‘", "’"
				}
				sb.WriteString(q)
				rec(x.Inlines)
				sb.WriteString(e)
			case *Emph:
				rec(x.Inlines)
			case *Underline:
				rec(x.Inlines)
			case *Strong:
				rec(x.Inlines)
			case *Strikeout:
				rec(x.Inlines)
			case *Superscript:
				rec(x.Inlines)
			case *Subscript:
				rec(x.Inlines)
			case *SmallCaps:
				rec(x.Inlines)
			case *Cite:
				rec(x.Inlines)
			case *Link:
				rec(x.Inlines)
			case *Image:
				rec(x.Inlines)
			case *Span:
				rec(x.Inlines)
			}
		}
	}
	rec(ins)
	return sb.String()
}

// Text splits s on whitespace into Str/Space inlines, so generated content
// is inert text in every writer (no markup is ever interpreted).
func Text(s string) []Inline {
	out := []Inline{}
	for i, w := range strings.Fields(s) {
		if i > 0 {
			out = append(out, &Space{})
		}
		out = append(out, &Str{Text: w})
	}
	return out
}

// NewAttr returns an empty attribute with non-nil slices.
func NewAttr(id string, classes ...string) Attr {
	if classes == nil {
		classes = []string{}
	}
	return Attr{ID: id, Classes: classes, KeyVals: [][2]string{}}
}

// Get returns the value of key in a.KeyVals.
func (a Attr) Get(key string) (string, bool) {
	for _, kv := range a.KeyVals {
		if kv[0] == key {
			return kv[1], true
		}
	}
	return "", false
}
