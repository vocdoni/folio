// SPDX-License-Identifier: AGPL-3.0-only

// Package pandocast holds minimal Go types for the pandoc JSON AST
// (pandoc-api-version 1.23.1.2), a strict decoder/encoder and walkers.
package pandocast

import "encoding/json"

// APIVersion is the only pandoc-api-version this package accepts and emits.
var APIVersion = [4]int{1, 23, 1, 2}

// MaxDecodeDepth bounds recursion while decoding (defence in depth; the
// validator enforces the configured nesting limit separately).
const MaxDecodeDepth = 512

// Document is a pandoc document.
type Document struct {
	Meta   map[string]json.RawMessage // kept opaque; always {} for user input
	Blocks []Block
}

// Attr is pandoc's (identifier, classes, key-value pairs).
type Attr struct {
	ID      string
	Classes []string
	KeyVals [][2]string
}

// Target is a link or image (url, title).
type Target struct {
	URL   string
	Title string
}

// Block is any pandoc block element.
type Block interface{ isBlock() }

// Inline is any pandoc inline element.
type Inline interface{ isInline() }

type (
	Plain     struct{ Inlines []Inline }
	Para      struct{ Inlines []Inline }
	LineBlock struct{ Lines [][]Inline }
	CodeBlock struct {
		Attr Attr
		Text string
	}
	RawBlock struct {
		Format string
		Text   string
	}
	BlockQuote  struct{ Blocks []Block }
	OrderedList struct {
		ListAttrs ListAttributes
		Items     [][]Block
	}
	BulletList     struct{ Items [][]Block }
	DefinitionList struct{ Items []DefinitionItem }
	Header         struct {
		Level   int
		Attr    Attr
		Inlines []Inline
	}
	HorizontalRule struct{}
	Table          struct {
		Attr     Attr
		Caption  Caption
		ColSpecs []ColSpec
		Head     TableHead
		Bodies   []TableBody
		Foot     TableFoot
	}
	Figure struct {
		Attr    Attr
		Caption Caption
		Blocks  []Block
	}
	Div struct {
		Attr   Attr
		Blocks []Block
	}
)

// ListAttributes is (start number, style, delimiter); style and delim are
// constructor names such as "Decimal" and "Period".
type ListAttributes struct {
	Start int
	Style string
	Delim string
}

// DefinitionItem is one (term, definitions) pair.
type DefinitionItem struct {
	Term []Inline
	Defs [][]Block
}

// Caption is (short caption or null, long caption). A nil Short encodes as null.
type Caption struct {
	Short []Inline
	Long  []Block
}

// ColSpec is (alignment, width). Width "" means ColWidthDefault; otherwise
// it is the exact JSON number text (no float64 round trip).
type ColSpec struct {
	Align string
	Width json.Number
}

type TableHead struct {
	Attr Attr
	Rows []Row
}

type TableBody struct {
	Attr           Attr
	RowHeadColumns int
	Head           []Row
	Body           []Row
}

type TableFoot struct {
	Attr Attr
	Rows []Row
}

type Row struct {
	Attr  Attr
	Cells []Cell
}

type Cell struct {
	Attr    Attr
	Align   string
	RowSpan int
	ColSpan int
	Blocks  []Block
}

type (
	Str         struct{ Text string }
	Emph        struct{ Inlines []Inline }
	Underline   struct{ Inlines []Inline }
	Strong      struct{ Inlines []Inline }
	Strikeout   struct{ Inlines []Inline }
	Superscript struct{ Inlines []Inline }
	Subscript   struct{ Inlines []Inline }
	SmallCaps   struct{ Inlines []Inline }
	Quoted      struct {
		QuoteType string // "SingleQuote" | "DoubleQuote"
		Inlines   []Inline
	}
	Cite struct {
		Citations []json.RawMessage // opaque; citations are disabled in Folio's dialect
		Inlines   []Inline
	}
	Code struct {
		Attr Attr
		Text string
	}
	Space     struct{}
	SoftBreak struct{}
	LineBreak struct{}
	Math      struct {
		MathType string // "InlineMath" | "DisplayMath"
		Text     string
	}
	RawInline struct {
		Format string
		Text   string
	}
	Link struct {
		Attr    Attr
		Inlines []Inline
		Target  Target
	}
	Image struct {
		Attr    Attr
		Inlines []Inline
		Target  Target
	}
	Note struct{ Blocks []Block }
	Span struct {
		Attr    Attr
		Inlines []Inline
	}
)

func (*Plain) isBlock()          {}
func (*Para) isBlock()           {}
func (*LineBlock) isBlock()      {}
func (*CodeBlock) isBlock()      {}
func (*RawBlock) isBlock()       {}
func (*BlockQuote) isBlock()     {}
func (*OrderedList) isBlock()    {}
func (*BulletList) isBlock()     {}
func (*DefinitionList) isBlock() {}
func (*Header) isBlock()         {}
func (*HorizontalRule) isBlock() {}
func (*Table) isBlock()          {}
func (*Figure) isBlock()         {}
func (*Div) isBlock()            {}

func (*Str) isInline()         {}
func (*Emph) isInline()        {}
func (*Underline) isInline()   {}
func (*Strong) isInline()      {}
func (*Strikeout) isInline()   {}
func (*Superscript) isInline() {}
func (*Subscript) isInline()   {}
func (*SmallCaps) isInline()   {}
func (*Quoted) isInline()      {}
func (*Cite) isInline()        {}
func (*Code) isInline()        {}
func (*Space) isInline()       {}
func (*SoftBreak) isInline()   {}
func (*LineBreak) isInline()   {}
func (*Math) isInline()        {}
func (*RawInline) isInline()   {}
func (*Link) isInline()        {}
func (*Image) isInline()       {}
func (*Note) isInline()        {}
func (*Span) isInline()        {}
