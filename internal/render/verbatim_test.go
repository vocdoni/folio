// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/vocdoni/folio/internal/model"
)

// TestComponentFiguresRenderVerbatim (A1): odd figures sent as strings reach
// the PDF text and the DOCX XML exactly as sent; Folio never parses, rounds or
// reformats them.
func TestComponentFiguresRenderVerbatim(t *testing.T) {
	p, tpl, brand := testPipeline(t)
	tpl.Components = append(tpl.Components, "milestones")
	figures := []string{"1.234,5 EUR", "TBD", "≈ 40 h", "€1,234.50", "1\u202f234,50\u00a0€"}
	data, _ := json.Marshal(map[string]any{
		"budget": map[string]any{
			"items": []any{
				map[string]any{"description": "Design", "quantity": figures[2], "unit_price": figures[1], "amount": figures[0]},
				map[string]any{"description": "Hosting", "amount": figures[4]},
			},
			"totals": []any{map[string]any{"label": "Total", "amount": figures[3], "emphasis": true}},
		},
		"milestones": []any{
			map[string]any{"name": "Kick-off", "duration": figures[2], "payment": figures[3]},
			map[string]any{"name": "Launch", "payment": figures[1]},
		},
	})
	in := model.DocumentInput{
		Markdown: "# Executive summary\n\nWe propose the work below.\n\n# Budget\n\n::: folio-budget\n:::\n\n::: folio-milestones\n:::\n",
		Metadata: json.RawMessage(`{"title":"Verbatim figures","date":"2026-10-18","language":"en"}`),
		Data:     data,
	}
	res, err := p.Render(context.Background(), Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: in})
	if err != nil {
		var me *model.Error
		if errors.As(err, &me) {
			t.Fatalf("%v %+v", err, me.Details)
		}
		t.Fatal(err)
	}
	text := squash(pdfText(t, res.PDF))
	docx := docxText(t, res.DOCX)
	for _, f := range figures {
		if !strings.Contains(text, squash(f)) {
			t.Errorf("PDF text lacks %q", f)
		}
		if !strings.Contains(docx, f) { // exact, including the no-break spaces
			t.Errorf("DOCX document.xml lacks %q", f)
		}
	}
}

// squash removes white space before comparing PDF text: a PDF carries glyphs,
// and a no-break space shares its glyph (and ToUnicode entry) with the space.
// The exact spaces are checked in the DOCX and in the component AST.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// docxText concatenates every w:t of word/document.xml in document order.
func docxText(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := zr.Open("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	var sb strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch x := tok.(type) {
		case xml.StartElement:
			inText = x.Name.Local == "t"
		case xml.EndElement:
			if x.Name.Local == "p" {
				sb.WriteByte('\n')
			}
			inText = false
		case xml.CharData:
			if inText {
				sb.Write(x)
			}
		}
	}
	return sb.String()
}

// pdfText returns the text shown on every page, decoded through each font's
// ToUnicode CMap (typst writes glyph ids, not characters), in content order.
func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	conf := pdfmodel.NewDefaultConfiguration()
	conf.ValidationMode = pdfmodel.ValidationRelaxed
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(pdf), conf)
	if err == nil {
		err = api.ValidateContext(context.Background(), ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for page := 1; page <= ctx.PageCount; page++ {
		d, _, inh, err := ctx.PageDict(context.Background(), page, false)
		if err != nil {
			t.Fatal(err)
		}
		content, err := ctx.PageContent(d, page)
		if err != nil {
			t.Fatal(err)
		}
		fonts := map[string]*cmap{}
		if fd, err := ctx.DereferenceDict(inh.Resources["Font"]); err == nil {
			for name, ref := range fd {
				font, err := ctx.DereferenceDict(ref)
				if err != nil || font["ToUnicode"] == nil {
					continue
				}
				sd, _, err := ctx.DereferenceStreamDict(font["ToUnicode"])
				if err != nil || sd == nil {
					continue
				}
				if err := sd.Decode(); err != nil {
					t.Fatal(err)
				}
				fonts[name] = parseCMap(sd.Content)
			}
		}
		showText(content, fonts, &sb)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// pdfToken is one content-stream token: an operator or operand.
type pdfToken struct {
	kind byte // 'o' operator, 's' string, 'n' name, '[' and ']' array bounds, 'x' other operand
	text string
}

// pdfTokens splits a content stream or CMap into tokens. Strings are
// returned as raw bytes (hex and literal forms decoded).
func pdfTokens(b []byte) []pdfToken {
	var out []pdfToken
	isDelim := func(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= 0 }
	isSpace := func(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0 }
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case isSpace(c):
			i++
		case c == '%':
			for i < len(b) && b[i] != '\n' && b[i] != '\r' {
				i++
			}
		case c == '(':
			var s []byte
			depth := 1
			for i++; i < len(b) && depth > 0; i++ {
				switch b[i] {
				case '\\':
					i++
					if i >= len(b) {
						break
					}
					switch e := b[i]; e {
					case 'n':
						s = append(s, '\n')
					case 'r':
						s = append(s, '\r')
					case 't':
						s = append(s, '\t')
					case 'b':
						s = append(s, '\b')
					case 'f':
						s = append(s, '\f')
					case '\r', '\n':
					default:
						if e >= '0' && e <= '7' {
							v, n := 0, 0
							for n < 3 && i < len(b) && b[i] >= '0' && b[i] <= '7' {
								v, i, n = v*8+int(b[i]-'0'), i+1, n+1
							}
							i--
							s = append(s, byte(v))
						} else {
							s = append(s, e)
						}
					}
				case '(':
					depth++
					s = append(s, '(')
				case ')':
					depth--
					if depth > 0 {
						s = append(s, ')')
					}
				default:
					s = append(s, b[i])
				}
			}
			out = append(out, pdfToken{'s', string(s)})
		case c == '<' && i+1 < len(b) && b[i+1] == '<':
			out, i = append(out, pdfToken{'x', "<<"}), i+2
		case c == '>' && i+1 < len(b) && b[i+1] == '>':
			out, i = append(out, pdfToken{'x', ">>"}), i+2
		case c == '<':
			j := bytes.IndexByte(b[i:], '>')
			if j < 0 {
				return out
			}
			h := strings.Map(func(r rune) rune {
				if strings.ContainsRune(" \n\r\t\f", r) {
					return -1
				}
				return r
			}, string(b[i+1:i+j]))
			if len(h)%2 == 1 {
				h += "0"
			}
			raw, _ := hex.DecodeString(h)
			out, i = append(out, pdfToken{'s', string(raw)}), i+j+1
		case c == '[' || c == ']':
			out, i = append(out, pdfToken{c, string(c)}), i+1
		case c == '/':
			j := i + 1
			for j < len(b) && !isSpace(b[j]) && !isDelim(b[j]) {
				j++
			}
			out, i = append(out, pdfToken{'n', string(b[i+1 : j])}), j
		default:
			j := i
			for j < len(b) && !isSpace(b[j]) && !isDelim(b[j]) {
				j++
			}
			if j == i {
				j++
			}
			w := string(b[i:j])
			kind := byte('o')
			if strings.IndexByte("+-.0123456789", w[0]) >= 0 || w == "true" || w == "false" || w == "null" {
				kind = 'x'
			}
			out, i = append(out, pdfToken{kind, w}), j
		}
	}
	return out
}

// cmap maps character codes (codeLen bytes each) to Unicode text.
type cmap struct {
	codeLen int
	m       map[string]string
}

func utf16Text(raw string) string {
	var u []uint16
	for i := 0; i+1 < len(raw); i += 2 {
		u = append(u, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return string(utf16.Decode(u))
}

func parseCMap(b []byte) *cmap {
	c := &cmap{codeLen: 1, m: map[string]string{}}
	toks := pdfTokens(b)
	for i := 0; i < len(toks); i++ {
		switch toks[i].text {
		case "begincodespacerange":
			if i+1 < len(toks) && toks[i+1].kind == 's' {
				c.codeLen = len(toks[i+1].text)
			}
		case "beginbfchar":
			for i++; i+1 < len(toks) && toks[i].text != "endbfchar"; i += 2 {
				c.m[toks[i].text] = utf16Text(toks[i+1].text)
			}
		case "beginbfrange":
			for i++; i+2 < len(toks) && toks[i].text != "endbfrange"; {
				lo, hi := []byte(toks[i].text), toks[i+1].text
				if toks[i+2].kind == '[' {
					j := i + 3
					for code := append([]byte(nil), lo...); j < len(toks) && toks[j].kind != ']'; j++ {
						c.m[string(code)] = utf16Text(toks[j].text)
						incr(code)
					}
					i = j + 1
					continue
				}
				dst := []byte(toks[i+2].text)
				for code := append([]byte(nil), lo...); ; incr(code) {
					c.m[string(code)] = utf16Text(string(dst))
					if string(code) == hi {
						break
					}
					incr(dst)
				}
				i += 3
			}
		}
	}
	return c
}

// incr adds one to a big-endian byte string.
func incr(b []byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

func (c *cmap) decode(s string) string {
	if c == nil {
		return s
	}
	var sb strings.Builder
	for i := 0; i+c.codeLen <= len(s); i += c.codeLen {
		sb.WriteString(c.m[s[i:i+c.codeLen]])
	}
	return sb.String()
}

// showText writes the text of the Tj, TJ, ' and " operators to sb.
func showText(content []byte, fonts map[string]*cmap, sb *strings.Builder) {
	var font *cmap
	var operands []pdfToken
	for _, tok := range pdfTokens(content) {
		if tok.kind != 'o' {
			operands = append(operands, tok)
			continue
		}
		switch tok.text {
		case "Tf":
			if len(operands) >= 2 {
				font = fonts[operands[len(operands)-2].text]
			}
		case "Tj", "'", "\"":
			if n := len(operands); n > 0 && operands[n-1].kind == 's' {
				sb.WriteString(font.decode(operands[n-1].text))
			}
		case "TJ":
			for _, o := range operands {
				if o.kind == 's' {
					sb.WriteString(font.decode(o.text))
				}
			}
		case "ET":
			sb.WriteByte('\n')
		}
		operands = operands[:0]
	}
}
