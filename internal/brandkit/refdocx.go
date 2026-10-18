// SPDX-License-Identifier: AGPL-3.0-only

// Package brandkit turns a brand into renderer inputs: the DOCX reference
// document (styles, header, footer, page setup), brand.json for Typst, and
// the resolved label table for a document language.
package brandkit

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/model"
)

// Logo is the PNG used in the DOCX header (SVG logos use their PNG rendition).
type Logo struct {
	PNG      []byte
	WidthPx  int
	HeightPx int
	WidthMM  int
}

// Page sizes in twips and the 20 mm margin.
const (
	a4W, a4H         = 11906, 16838
	letterW, letterH = 12240, 15840
	marginTwips      = 1134 // 20 mm
	emuPerMM         = 36000
)

var (
	styleRE  = regexp.MustCompile(`(?s)<w:style\b[^>]*w:styleId="([^"]+)"[^>]*>.*?</w:style>`)
	rFontsRE = regexp.MustCompile(`<w:rFonts\b[^>]*/>`)
	colorRE  = regexp.MustCompile(`<w:color\b[^>]*/>`)
	rPrRE    = regexp.MustCompile(`(?s)<w:rPr>.*?</w:rPr>`)
	sectRE   = regexp.MustCompile(`(?s)<w:sectPr\b[^>]*>.*?</w:sectPr>`)
)

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func rFonts(family string) string {
	f := xmlEscape(family)
	return fmt.Sprintf(`<w:rFonts w:ascii="%[1]s" w:hAnsi="%[1]s" w:eastAsia="%[1]s" w:cs="%[1]s" />`, f)
}

// headingStyles get the heading font and primary colour; editing by styleId
// keeps Hyperlink and other accent-coloured styles untouched.
var headingStyles = map[string]bool{"Title": true, "Subtitle": true, "TOCHeading": true,
	"Heading1": true, "Heading2": true, "Heading3": true, "Heading4": true, "Heading5": true,
	"Heading6": true, "Heading7": true, "Heading8": true, "Heading9": true}

func editStyles(st string, b model.Brand) (string, error) {
	// Body font on docDefaults.
	defaults := `<w:rFonts w:asciiTheme="minorHAnsi" w:eastAsiaTheme="minorEastAsia" w:hAnsiTheme="minorHAnsi" w:cstheme="minorBidi" />`
	if !strings.Contains(st, defaults) {
		return "", fmt.Errorf("brandkit: reference styles.xml has unexpected docDefaults")
	}
	st = strings.Replace(st, defaults, rFonts(b.Fonts.Body), 1)
	color := strings.TrimPrefix(b.Colors.Primary, "#")
	st = styleRE.ReplaceAllStringFunc(st, func(s string) string {
		id := styleRE.FindStringSubmatch(s)[1]
		switch {
		case headingStyles[id]:
			s = rFontsRE.ReplaceAllString(s, "")
			s = colorRE.ReplaceAllString(s, "")
			ins := rFonts(b.Fonts.Heading) + fmt.Sprintf(`<w:color w:val="%s" />`, color)
			if rPrRE.MatchString(s) {
				return strings.Replace(s, "<w:rPr>", "<w:rPr>"+ins, 1)
			}
			return strings.Replace(s, "</w:style>", "<w:rPr>"+ins+"</w:rPr></w:style>", 1)
		case id == "VerbatimChar":
			return rFontsRE.ReplaceAllString(s, rFonts(b.Fonts.Mono))
		}
		return s
	})
	return st, nil
}

// fontTable adds entries with w:altName so Word substitutes the brand's
// fallback fonts when the bundled fonts are not installed.
func fontTable(ft string, b model.Brand) string {
	var add strings.Builder
	families := map[string]string{"heading": b.Fonts.Heading, "body": b.Fonts.Body, "mono": b.Fonts.Mono}
	seen := map[string]bool{}
	for _, role := range []string{"heading", "body", "mono"} {
		family := families[role]
		if seen[family] {
			continue
		}
		seen[family] = true
		alt := b.DOCX.FontFallbacks[role]
		if family == "" || strings.Contains(ft, `w:name="`+xmlEscape(family)+`"`) {
			continue
		}
		add.WriteString(`<w:font w:name="` + xmlEscape(family) + `">`)
		if alt != "" {
			add.WriteString(`<w:altName w:val="` + xmlEscape(alt) + `"/>`)
		}
		add.WriteString(`</w:font>`)
	}
	return strings.Replace(ft, "</w:fonts>", add.String()+"</w:fonts>", 1)
}

const partNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"`

func run(text string) string {
	return `<w:r><w:rPr><w:sz w:val="18"/></w:rPr><w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r>`
}

func headerXML(b model.Brand, logo *Logo) string {
	var p strings.Builder
	p.WriteString(`<w:p><w:pPr><w:pStyle w:val="Header"/><w:tabs><w:tab w:val="right" w:pos="9638"/></w:tabs></w:pPr>`)
	if logo != nil && b.Header.ShowLogo {
		cx := int64(logo.WidthMM) * emuPerMM
		cy := cx * int64(logo.HeightPx) / int64(max(logo.WidthPx, 1))
		fmt.Fprintf(&p, `<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%[1]d" cy="%[2]d"/><wp:docPr id="1" name="Logo" descr="%[3]s"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="1" name="logo.png"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="rIdLogo"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%[1]d" cy="%[2]d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
			cx, cy, xmlEscape(b.Name))
	}
	if b.Header.Text != "" {
		p.WriteString(`<w:r><w:tab/></w:r>` + run(b.Header.Text))
	}
	p.WriteString(`</w:p>`)
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:hdr ` + partNS + `>` + p.String() + `</w:hdr>`
}

func footerXML(b model.Brand, pageLabel string) string {
	var p strings.Builder
	p.WriteString(`<w:p><w:pPr><w:pStyle w:val="Footer"/><w:tabs><w:tab w:val="right" w:pos="9638"/></w:tabs></w:pPr>`)
	p.WriteString(run(b.Footer.Text))
	if b.Footer.PageNumbers {
		p.WriteString(`<w:r><w:tab/></w:r>` + run(pageLabel+" "))
		for _, fld := range []string{`<w:fldChar w:fldCharType="begin"/>`, `<w:instrText xml:space="preserve"> PAGE </w:instrText>`,
			`<w:fldChar w:fldCharType="separate"/>`, `<w:t>1</w:t>`, `<w:fldChar w:fldCharType="end"/>`} {
			p.WriteString(`<w:r><w:rPr><w:sz w:val="18"/></w:rPr>` + fld + `</w:r>`)
		}
	}
	p.WriteString(`</w:p>`)
	if b.Footer.Legal != "" {
		p.WriteString(`<w:p><w:pPr><w:pStyle w:val="Footer"/></w:pPr>` + run(b.Footer.Legal) + `</w:p>`)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:ftr ` + partNS + `>` + p.String() + `</w:ftr>`
}

// ReferenceDOCX rewrites pandoc's default reference.docx (base) for a brand:
// fonts and heading colours (by styleId), font fallbacks, header (logo +
// text) and footer (text, page number, legal line) parts with their content
// types and relationships, and the section page size with 20 mm margins.
// sectPr children are ordered headerReference, footerReference, footnotePr,
// pgSz, pgMar as Word requires. Output is deterministic (fixed zip times).
func ReferenceDOCX(base []byte, b model.Brand, logo *Logo, pageSize, pageLabel string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		return nil, fmt.Errorf("brandkit: read reference.docx: %w", err)
	}
	files := map[string][]byte{}
	var order []string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("brandkit: open %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("brandkit: read %s: %w", f.Name, err)
		}
		files[f.Name] = data
		order = append(order, f.Name)
	}
	put := func(name string, data []byte) {
		if _, ok := files[name]; !ok {
			order = append(order, name)
		}
		files[name] = data
	}
	st, err := editStyles(string(files["word/styles.xml"]), b)
	if err != nil {
		return nil, err
	}
	put("word/styles.xml", []byte(st))
	put("word/fontTable.xml", []byte(fontTable(string(files["word/fontTable.xml"]), b)))
	put("word/header1.xml", []byte(headerXML(b, logo)))
	put("word/footer1.xml", []byte(footerXML(b, pageLabel)))
	ct := string(files["[Content_Types].xml"])
	if logo != nil && b.Header.ShowLogo {
		put("word/media/folio-logo.png", logo.PNG)
		put("word/_rels/header1.xml.rels", []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdLogo" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/folio-logo.png"/></Relationships>`))
		if !strings.Contains(ct, `Extension="png"`) {
			ct = strings.Replace(ct, "<Default ", `<Default Extension="png" ContentType="image/png" /><Default `, 1)
		}
	}
	ct = strings.Replace(ct, "</Types>", `<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml" /><Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml" /></Types>`, 1)
	put("[Content_Types].xml", []byte(ct))
	rels := strings.Replace(string(files["word/_rels/document.xml.rels"]), "</Relationships>",
		`<Relationship Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Id="rIdFolioHeader" Target="header1.xml" /><Relationship Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Id="rIdFolioFooter" Target="footer1.xml" /></Relationships>`, 1)
	put("word/_rels/document.xml.rels", []byte(rels))
	w, h := a4W, a4H
	if pageSize == "letter" {
		w, h = letterW, letterH
	}
	sect := fmt.Sprintf(`<w:sectPr><w:headerReference w:type="default" r:id="rIdFolioHeader" /><w:footerReference w:type="default" r:id="rIdFolioFooter" /><w:footnotePr><w:numRestart w:val="eachSect" /></w:footnotePr><w:pgSz w:w="%d" w:h="%d" /><w:pgMar w:top="%[3]d" w:right="%[3]d" w:bottom="%[3]d" w:left="%[3]d" w:header="709" w:footer="709" w:gutter="0" /></w:sectPr>`, w, h, marginTwips)
	doc := string(files["word/document.xml"])
	if !sectRE.MatchString(doc) {
		return nil, fmt.Errorf("brandkit: reference document.xml has no sectPr")
	}
	put("word/document.xml", []byte(sectRE.ReplaceAllLiteralString(doc, sect)))

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	names := append([]string{"[Content_Types].xml"}, without(order, "[Content_Types].xml")...)
	for _, n := range names {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: fixed})
		if err != nil {
			return nil, fmt.Errorf("brandkit: zip: %w", err)
		}
		if _, err := fw.Write(files[n]); err != nil {
			return nil, fmt.Errorf("brandkit: zip: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("brandkit: zip: %w", err)
	}
	return buf.Bytes(), nil
}

func without(xs []string, x string) []string {
	out := make([]string, 0, len(xs))
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
