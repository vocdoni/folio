// SPDX-License-Identifier: AGPL-3.0-only

package pandocast

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

const dialect = "markdown-raw_html-raw_tex-raw_attribute-yaml_metadata_block-citations-implicit_header_references"

func pandocBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FOLIO_PANDOC"); p != "" {
		return p
	}
	p, err := exec.LookPath("pandoc")
	if err != nil {
		t.Skip("SKIPPING: pandoc not found; set FOLIO_PANDOC to the pinned pandoc 3.12.1 binary")
	}
	return p
}

func run(t *testing.T, bin string, stdin []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, stderr.String())
	}
	return out
}

func TestRoundTripAgainstPandoc(t *testing.T) {
	bin := pandocBin(t)
	md, err := os.ReadFile(filepath.Join("testdata", "kitchen-sink.md"))
	if err != nil {
		t.Fatal(err)
	}
	orig := run(t, bin, md, "--sandbox", "-f", dialect, "-t", "json")
	doc, err := Decode(orig)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	enc, err := Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var a, b any
	if err := json.Unmarshal(orig, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(enc, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("round trip differs\norig: %s\nenc:  %s", orig, enc)
	}
	// pandoc must accept our encoding and produce the same native output.
	n1 := run(t, bin, orig, "--sandbox", "-f", "json", "-t", "native")
	n2 := run(t, bin, enc, "--sandbox", "-f", "json", "-t", "native")
	if !bytes.Equal(n1, n2) {
		t.Fatalf("native output differs")
	}
}

func TestDecodeRejectsUnknownAndWrongVersion(t *testing.T) {
	cases := map[string]string{
		"version": `{"pandoc-api-version":[1,22,0],"meta":{},"blocks":[]}`,
		"block":   `{"pandoc-api-version":[1,23,1,2],"meta":{},"blocks":[{"t":"Bogus"}]}`,
		"inline":  `{"pandoc-api-version":[1,23,1,2],"meta":{},"blocks":[{"t":"Para","c":[{"t":"Bogus"}]}]}`,
		"arity":   `{"pandoc-api-version":[1,23,1,2],"meta":{},"blocks":[{"t":"Header","c":[1]}]}`,
	}
	for name, in := range cases {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDecodeDepthLimit(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"pandoc-api-version":[1,23,1,2],"meta":{},"blocks":[`)
	for i := 0; i < MaxDecodeDepth+10; i++ {
		buf.WriteString(`{"t":"BlockQuote","c":[`)
	}
	for i := 0; i < MaxDecodeDepth+10; i++ {
		buf.WriteString(`]}`)
	}
	buf.WriteString(`]}`)
	if _, err := Decode(buf.Bytes()); err == nil {
		t.Fatal("expected depth error")
	}
}

func TestWalkDepthAndMutation(t *testing.T) {
	doc := &Document{Blocks: []Block{
		&BlockQuote{Blocks: []Block{&Para{Inlines: []Inline{&Image{Attr: NewAttr(""), Target: Target{URL: "asset:x"}}}}}},
	}}
	maxDepth := 0
	Walk(doc.Blocks, Visitor{
		Block: func(b Block, d int) bool {
			if d > maxDepth {
				maxDepth = d
			}
			return true
		},
		Inline: func(in Inline, d int) bool {
			if im, ok := in.(*Image); ok {
				im.Target.URL = "assets/x.png"
			}
			return true
		},
	})
	if maxDepth != 1 {
		t.Fatalf("depth = %d, want 1", maxDepth)
	}
	img := doc.Blocks[0].(*BlockQuote).Blocks[0].(*Para).Inlines[0].(*Image)
	if img.Target.URL != "assets/x.png" {
		t.Fatalf("mutation lost: %q", img.Target.URL)
	}
}

func TestStringify(t *testing.T) {
	got := Stringify([]Inline{&Str{Text: "Executive"}, &Space{}, &Emph{Inlines: []Inline{&Str{Text: "summary"}}}})
	if got != "Executive summary" {
		t.Fatalf("got %q", got)
	}
}
