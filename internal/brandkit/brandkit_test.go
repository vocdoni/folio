// SPDX-License-Identifier: AGPL-3.0-only

package brandkit

import (
	"archive/zip"
	"bytes"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/model"
)

func testBrand() model.Brand {
	return model.Brand{ID: "example", Name: "Example Co",
		Colors: model.BrandColors{Primary: "#1F4FD8", Secondary: "#0B1F44", Accent: "#F2A900", Text: "#1A1A1A", Muted: "#6B7280", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Source Serif 4", Mono: "JetBrains Mono"},
		Logo:   &model.BrandLogo{AssetID: "x", WidthMM: 35},
		Header: model.BrandHeader{Text: "Example & Co", ShowLogo: true},
		Footer: model.BrandFooter{Text: "Example Co", Legal: "Example Co S.L.", PageNumbers: true},
		DOCX:   model.BrandDOCX{FontFallbacks: map[string]string{"heading": "Arial", "body": "Georgia"}}}
}

func unzip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		d, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(d)
	}
	return out
}

func TestReferenceDOCX(t *testing.T) {
	base, err := bundled.ReferenceDOCX()
	if err != nil {
		t.Fatal(err)
	}
	logo := &Logo{PNG: []byte("\x89PNG fake"), WidthPx: 625, HeightPx: 188, WidthMM: 35}
	a, err := ReferenceDOCX(base, testBrand(), logo, "letter", "Page")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ReferenceDOCX(base, testBrand(), logo, "letter", "Page")
	if !bytes.Equal(a, b) {
		t.Fatal("reference.docx is not deterministic")
	}
	files := unzip(t, a)
	styles := files["word/styles.xml"]
	h1 := regexp.MustCompile(`(?s)<w:style\b[^>]*w:styleId="Heading1".*?</w:style>`).FindString(styles)
	if !strings.Contains(h1, `w:ascii="Inter"`) || !strings.Contains(h1, `w:val="1F4FD8"`) {
		t.Fatalf("Heading1 not branded: %s", h1)
	}
	link := regexp.MustCompile(`(?s)<w:style\b[^>]*w:styleId="Hyperlink".*?</w:style>`).FindString(styles)
	if strings.Contains(link, "1F4FD8") {
		t.Fatal("Hyperlink recoloured (styles must be edited by styleId)")
	}
	if !strings.Contains(files["word/fontTable.xml"], `<w:font w:name="Inter"><w:altName w:val="Arial"/>`) {
		t.Fatal("font fallback missing")
	}
	for _, part := range []string{"word/header1.xml", "word/footer1.xml", "word/media/folio-logo.png", "word/_rels/header1.xml.rels"} {
		if _, ok := files[part]; !ok {
			t.Errorf("missing %s", part)
		}
	}
	if !strings.Contains(files["word/header1.xml"], "Example &amp; Co") {
		t.Error("header text not escaped")
	}
	ct := files["[Content_Types].xml"]
	if !strings.Contains(ct, `/word/header1.xml`) || !strings.Contains(ct, `Extension="png"`) {
		t.Error("content types missing header/png")
	}
	sect := regexp.MustCompile(`(?s)<w:sectPr>.*</w:sectPr>`).FindString(files["word/document.xml"])
	order := regexp.MustCompile(`<w:(headerReference|footerReference|footnotePr|pgSz|pgMar)\b`).FindAllStringSubmatch(sect, -1)
	var got []string
	for _, m := range order {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != "headerReference,footerReference,footnotePr,pgSz,pgMar" || !strings.Contains(sect, `w:w="12240"`) {
		t.Fatalf("sectPr = %s", sect)
	}
}

func TestResolveLabels(t *testing.T) {
	l := ResolveLabels("es", map[string]map[string]string{"es": {"budget.amount": "Importe total", "budget.total": "x", "bogus": "x"}})
	if l["budget.amount"] != "Importe total" || l["budget.unit_price"] != "Precio unitario" || l["bogus"] != "" || l["budget.total"] != "" {
		t.Fatalf("es: %v", l)
	}
	l = ResolveLabels("fr", map[string]map[string]string{"fr": {"budget.amount": "Montant"}})
	if l["budget.amount"] != "Amount" {
		t.Fatalf("incomplete fr must fall back to en: %v", l["budget.amount"])
	}
	full := map[string]string{"budget.total": "Total TTC"}
	for _, k := range LabelKeys {
		full[k] = "fr-" + k
	}
	l = ResolveLabels("fr", map[string]map[string]string{"fr": full})
	if l["budget.amount"] != "fr-budget.amount" || l["budget.total"] != "" || len(l) != len(LabelKeys) {
		t.Fatalf("complete fr: %v", l)
	}
}

// The budget total-row labels were retired with Folio-computed totals: they
// are no longer label keys, but templates that carry them stay valid.
func TestRetiredLabelKeys(t *testing.T) {
	for _, k := range RetiredLabelKeys {
		if slices.Contains(LabelKeys, k) {
			t.Errorf("%s is still a label key", k)
		}
	}
	if len(RetiredLabelKeys) != 3 {
		t.Fatalf("retired keys = %v", RetiredLabelKeys)
	}
}

func TestBrandJSONReplacesAssetID(t *testing.T) {
	raw, err := BrandJSON(testBrand(), "assets/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "asset_id") || !strings.Contains(s, `"logo":{"path":"assets/logo.svg","width_mm":35}`) {
		t.Fatalf("brand.json = %s", s)
	}
}
