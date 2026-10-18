// SPDX-License-Identifier: AGPL-3.0-only

package checks

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/vocdoni/folio/internal/model"
)

// a4 at 144 ppi
const pw, ph = 1191, 1684

func page(t *testing.T, draw func(img *image.RGBA)) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			img.Set(x, y, color.White)
		}
	}
	draw(img)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func rect(img *image.RGBA, x0, y0, x1, y1 int) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, color.Black)
		}
	}
}

func codes(fs []model.Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Code] = f.Page
	}
	return m
}

func TestPagesDetectBlankAndEdgeInk(t *testing.T) {
	normal := page(t, func(img *image.RGBA) { rect(img, 200, 300, 900, 900) })
	blank := page(t, func(img *image.RGBA) { rect(img, 200, 20, 400, 60) })     // header band only
	overflow := page(t, func(img *image.RGBA) { rect(img, 200, 300, pw, 360) }) // runs off the right edge
	fs, err := Pages([][]byte{normal, blank, overflow}, DefaultPageOptions())
	if err != nil {
		t.Fatal(err)
	}
	got := codes(fs)
	if got[model.CodeBlankPage] != 2 || got[model.CodeEdgeInk] != 3 || len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestPDFValidityAndPageLimit(t *testing.T) {
	pdf, err := os.ReadFile("testdata/two-pages.pdf")
	if err != nil {
		t.Fatal(err)
	}
	n, fs := PDF(context.Background(), pdf, 200)
	if n != 2 || len(fs) != 0 {
		t.Fatalf("n=%d fs=%+v", n, fs)
	}
	if _, fs := PDF(context.Background(), pdf, 1); len(fs) != 1 || fs[0].Code != model.CodeTooManyPages {
		t.Fatalf("page limit: %+v", fs)
	}
	if _, fs := PDF(context.Background(), []byte("%PDF-1.7\ngarbage"), 200); len(fs) != 1 || fs[0].Code != model.CodePDFInvalid {
		t.Fatalf("garbage: %+v", fs)
	}
}

func TestDOCXStructure(t *testing.T) {
	if fs := DOCX([]byte("not a zip")); len(fs) != 1 {
		t.Fatal("non-zip accepted")
	}
}
