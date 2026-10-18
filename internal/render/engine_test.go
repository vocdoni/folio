// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
	"github.com/vocdoni/folio/internal/sandbox"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// testEngine builds an Engine with the pinned tools; it skips loudly when
// they are missing.
func testEngine(t *testing.T) *Engine {
	t.Helper()
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		t.Skipf("SKIPPING: %v — set FOLIO_PANDOC to pandoc 3.12.1", err)
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		t.Skipf("SKIPPING: %v — set FOLIO_TYPST to typst 0.15.1", err)
	}
	rt, err := bundled.Materialize(filepath.Join(os.TempDir(), "folio-test-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	l := sandbox.DefaultLimits()
	l.Timeout = 30 * time.Second
	e, err := NewEngine(EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Self: self, JobRoot: t.TempDir(), Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

const testLayout = `#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = {
  set page(paper: "a4", margin: 20mm)
  set text(font: "Source Serif 4", size: 11pt)
  show math.equation: set text(font: "` + bundled.MathFont + `")
  body
}
`

func writeInputs(t *testing.T, e *Engine, job *Job) {
	t.Helper()
	err := e.WriteTypstInputs(job, TypstInputs{
		LayoutSource: testLayout,
		Meta:         []byte(`{"title":"T","date":"2026-10-18","language":"en"}`),
		Brand:        []byte(`{}`), Labels: []byte(`{}`), Options: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEngineMarkdownToPDFAndDOCX(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()
	job, err := e.NewJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Remove()
	md := "# Title\n\nSome *text* and $E=mc^2$.\n\n![Logo](asset:0192abcd-0000-7000-8000-000000000000)\n"
	doc, warns, err := e.ParseMarkdown(ctx, job, md)
	if err != nil || len(warns) != 0 {
		t.Fatalf("parse: %v %v", err, warns)
	}
	png, err := e.SVGToPNG(ctx, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#1F4FD8"/></svg>`))
	if err != nil {
		t.Fatalf("svg2png: %v", err)
	}
	pandocast.Walk(doc.Blocks, pandocast.Visitor{Inline: func(in pandocast.Inline, _ int) bool {
		if im, ok := in.(*pandocast.Image); ok {
			im.Target.URL = "assets/0192abcd-0000-7000-8000-000000000000.png"
		}
		return true
	}})
	if err := job.WriteFile("assets/0192abcd-0000-7000-8000-000000000000.png", png); err != nil {
		t.Fatal(err)
	}
	ref, _ := bundled.ReferenceDOCX()
	docx, err := e.WriteDOCX(ctx, job, doc, DOCXOptions{ReferenceDOCX: ref, TOC: true, TOCTitle: "Contents", Epoch: 1792281600})
	if err != nil {
		t.Fatalf("docx: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	media := false
	for _, f := range zr.File {
		media = media || strings.HasPrefix(f.Name, "word/media/")
	}
	if !media {
		t.Fatal("image not embedded in DOCX")
	}
	if err := e.WriteTypstBody(ctx, job, doc); err != nil {
		t.Fatalf("typst body: %v", err)
	}
	writeInputs(t, e, job)
	pdf, pages, tw, err := e.CompileTypst(ctx, job, 1792281600)
	if err != nil {
		t.Fatalf("typst: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pages) != 1 || len(tw) != 0 {
		t.Fatalf("pdf=%q pages=%d warnings=%v", pdf[:8], len(pages), tw)
	}
	// AC5: the job dir never contains a symlink.
	_ = filepath.WalkDir(job.Dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("symlink in job dir: %s", p)
		}
		return nil
	})
	p1, err := e.RasterizePDFPage(ctx, pdf, 1, 72)
	if err != nil || !bytes.HasPrefix(p1, []byte("\x89PNG")) {
		t.Fatalf("rasterize: %v", err)
	}
}

func TestSecurityRawTypstIsInert(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()
	job, _ := e.NewJob()
	defer job.Remove()
	md := "```{=typst}\n#read(\"/etc/passwd\")\n```\n\n<b>html</b> \\textbf{tex} `#read(\"/etc/hostname\")`{=typst}\n"
	doc, _, err := e.ParseMarkdown(ctx, job, md)
	if err != nil {
		t.Fatal(err)
	}
	pandocast.Walk(doc.Blocks, pandocast.Visitor{
		Block: func(b pandocast.Block, _ int) bool {
			if _, ok := b.(*pandocast.RawBlock); ok {
				t.Error("RawBlock in parsed AST")
			}
			return true
		},
		Inline: func(in pandocast.Inline, _ int) bool {
			if _, ok := in.(*pandocast.RawInline); ok {
				t.Error("RawInline in parsed AST")
			}
			return true
		},
	})
	if err := e.WriteTypstBody(ctx, job, doc); err != nil {
		t.Fatal(err)
	}
	body, _ := job.ReadFile("body.typ")
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#read") {
			t.Fatalf("typst code survived as markup: %q", line)
		}
	}
}

func TestSecurityJobFilesRefuseSymlinks(t *testing.T) {
	job, err := NewJob(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(job.Dir, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := job.WriteFile("evil", []byte("x")); err == nil {
		t.Error("WriteFile followed a symlink")
	}
	if _, err := job.ReadFile("evil"); err == nil {
		t.Error("ReadFile followed a symlink")
	}
	if err := os.Symlink("/etc", filepath.Join(job.Dir, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := job.WriteFile("dir/x", []byte("x")); err == nil {
		t.Error("WriteFile traversed a symlinked directory")
	}
	for _, bad := range []string{"../x", "/etc/x", "a/../../x", ""} {
		if err := job.WriteFile(bad, nil); err == nil {
			t.Errorf("WriteFile(%q) accepted", bad)
		}
	}
}

func TestSecurityPreviewImports(t *testing.T) {
	lit := `#import "@preview/cetz:0.3.0": *` + "\n" + `#import "/base/proposal.typ": folio-layout`
	f := ScanTypstImports(lit, nil)
	if len(f) != 1 || !strings.Contains(f[0].Message, "@preview/cetz:0.3.0") {
		t.Fatalf("literal import findings = %+v", f)
	}
	if f := ScanTypstImports(`#import ("@pre" + "view/cetz:0.3.0"): *`, nil); len(f) != 1 {
		t.Fatalf("expression import not flagged: %+v", f)
	}
	if f := ScanTypstImports(`#import "@preview/hello:0.1.0": hello`, []string{"@preview/hello:0.1.0"}); len(f) != 0 {
		t.Fatalf("allowlisted import flagged: %+v", f)
	}
	if f := ScanTypstImports("[Prices include VAT. Import duties excluded.]", nil); len(f) != 0 {
		t.Fatalf("prose flagged: %+v", f)
	}
	for _, bad := range []string{`#include "../x.typ"`, `#import "/etc/x.typ"`, `#import "https://x/y.typ"`, "  #import cetz: *", "x #include p"} {
		if f := ScanTypstImports(bad, nil); len(f) != 1 {
			t.Errorf("%s not flagged", bad)
		}
	}

	// The expression form bypasses the scan; the sandbox must stop it.
	e := testEngine(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
			got <- struct{}{}
		}
	}()
	probe := "http://" + ln.Addr().String()
	e.cfg.ExtraEnv = []string{"HTTPS_PROXY=" + probe, "https_proxy=" + probe, "HTTP_PROXY=" + probe, "http_proxy=" + probe}
	job, _ := e.NewJob()
	defer job.Remove()
	if err := job.WriteFile("body.typ", []byte(`#import ("@pre" + "view/cetz:0.3.0"): *`)); err != nil {
		t.Fatal(err)
	}
	writeInputs(t, e, job)
	_, _, _, err = e.CompileTypst(context.Background(), job, 0)
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrRenderFailed {
		t.Fatalf("err = %v, want render_failed", err)
	}
	if sandbox.ProbeABI() < 4 {
		t.Logf("Landlock ABI < 4: TCP is not denied by the kernel here; the dead proxy is the only control")
		return
	}
	select {
	case <-got:
		t.Fatal("typst reached the network from the sandbox")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSecurityTypstInfiniteLoopKilledAtTimeout(t *testing.T) {
	e := testEngine(t)
	e.cfg.Limits.Timeout = 2 * time.Second
	job, _ := e.NewJob()
	defer job.Remove()
	loop := "#{\n  let x = 0\n  for i in range(100000) { for j in range(100000) { x += 1 } }\n  [#x]\n}\n"
	if err := job.WriteFile("body.typ", []byte(loop)); err != nil {
		t.Fatal(err)
	}
	writeInputs(t, e, job)
	start := time.Now()
	_, _, _, err := e.CompileTypst(context.Background(), job, 0)
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrTimeout {
		t.Fatalf("err = %v, want timeout", err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestQueueBusy(t *testing.T) {
	q := NewQueue(2, 3)
	ctx := context.Background()
	var releases []func()
	for i := 0; i < 2; i++ {
		r, err := q.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, r)
	}
	acquired := make(chan func(), 3)
	for i := 0; i < 3; i++ {
		go func() {
			r, err := q.Acquire(ctx)
			if err == nil {
				acquired <- r
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		q.mu.Lock()
		w := q.waiting
		q.mu.Unlock()
		if w == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting = %d", w)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := q.Acquire(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	for _, r := range releases {
		r()
	}
	for i := 0; i < 3; i++ {
		(<-acquired)()
	}
}

// TestVendoredPackageMechanism: the allowlist is the content of the package
// dir; an allowlisted package compiles offline from it.
func TestVendoredPackageMechanism(t *testing.T) {
	e := testEngine(t)
	dir, err := filepath.Abs(filepath.Join("testdata", "packages"))
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.PackagesDir = dir
	allow, err := bundled.VendoredPackages(dir)
	if err != nil || len(allow) != 1 || allow[0] != "@preview/hello:0.1.0" {
		t.Fatalf("allowlist = %v, %v", allow, err)
	}
	src := `#import "@preview/hello:0.1.0": hello` + "\n#hello()\n"
	if f := ScanTypstImports(src, allow); len(f) != 0 {
		t.Fatalf("allowlisted import flagged: %+v", f)
	}
	job, _ := e.NewJob()
	defer job.Remove()
	if err := job.WriteFile("body.typ", []byte(src)); err != nil {
		t.Fatal(err)
	}
	writeInputs(t, e, job)
	if _, _, _, err := e.CompileTypst(context.Background(), job, 0); err != nil {
		t.Fatalf("vendored package did not compile: %v", err)
	}
}
