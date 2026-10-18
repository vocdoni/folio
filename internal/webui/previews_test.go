// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/model"
)

// TestTemplatePreview (F1, B-AC4): an unsaved spec renders to in-memory PNGs
// (pdf only) without files rows or blobs; identical specs hit the cache; a
// concurrent second preview of the session gets 409; another session cannot
// read the pages; invalid specs come back as findings.
func TestTemplatePreview(t *testing.T) {
	pr := newFakeRenderer(t)
	sv := newServices(t, func(o *Options) {
		c := *o.Catalog
		c.Renderer = pr
		o.Preview = &c
	})
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	rows := func() (n int) {
		_ = sv.opts.DB.R.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM files) + (SELECT count(*) FROM templates)`).Scan(&n)
		return n
	}
	blobs := func() int {
		m, _ := filepath.Glob(filepath.Join(sv.blobDir, "sha256", "*", "*"))
		return len(m)
	}
	before, beforeBlobs := rows(), blobs()
	spec := templateSpec("draft", "acme")
	out := decode[PreviewOut](t, sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: spec}, nil), 200)
	if !out.OK || out.Pages != 2 || len(out.PageURLs) != 2 || out.PreviewID == "" || out.Cached {
		t.Fatalf("preview %+v", out)
	}
	if f, _ := pr.formats.Load().([]model.Format); len(f) != 1 || f[0] != model.FormatPDF {
		t.Fatalf("preview formats %v, want [pdf]", f)
	}
	if rows() != before || blobs() != beforeBlobs {
		t.Fatal("a preview must not create files rows, versions or blobs")
	}
	if w := sv.do("GET", out.PageURLs[0], nil, nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("page: %d", w.Code)
	}
	again := decode[PreviewOut](t, sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: spec}, nil), 200)
	if !again.Cached || again.PreviewID != out.PreviewID || pr.calls.Load() != 1 {
		t.Fatalf("identical spec must hit the cache: %+v calls %d", again, pr.calls.Load())
	}
	// Another session cannot read this session's preview.
	other := newFixture(t, nil)
	other.s = sv.s
	other.login()
	if w := other.do("GET", out.PageURLs[0], nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("cross-session read: %d", w.Code)
	}
	// One preview in flight per session.
	pr.gate = make(chan struct{})
	spec2 := spec
	spec2.Name = "Memo 2"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: spec2}, nil) }()
	for pr.calls.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	spec3 := spec
	spec3.Name = "Memo 3"
	if w := sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: spec3}, nil); w.Code != http.StatusConflict || errorCode(t, w) != "preview_in_progress" {
		t.Fatalf("concurrent preview: %d %s", w.Code, w.Body)
	}
	close(pr.gate)
	if w := <-done; w.Code != 200 {
		t.Fatalf("first preview: %d", w.Code)
	}
	pr.gate = nil
	bad := spec
	bad.Layout.Base = "poster"
	inv := decode[PreviewOut](t, sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: bad}, nil), 200)
	if inv.OK || len(inv.Findings) == 0 || inv.Findings[0].Path != "/layout/base" {
		t.Fatalf("invalid spec: %+v", inv)
	}
	failing := spec
	failing.Sample.Markdown = "FAIL"
	if f := decode[PreviewOut](t, sv.do("POST", "/ui/api/templates/preview", PreviewIn{Spec: failing}, nil), 200); f.OK || !strings.Contains(f.Findings[0].Message, "Scope") {
		t.Fatalf("failing sample: %+v", f)
	}
	// Logout drops the session's previews.
	sv.do("POST", "/ui/api/logout", nil, nil)
	if _, ok := sv.s.previews.page(out.PreviewID, keyOf(sv.cookie), 1); ok {
		t.Fatal("previews survived logout")
	}
}

func TestPreviewCacheBounds(t *testing.T) {
	clk := &testClock{t: time.Unix(0, 0)}
	c := newPreviewCache(clk.Now)
	for i := 0; i < previewsPerSession+2; i++ {
		c.put(&preview{id: string(rune('a' + i)), session: "s1", created: clk.Now(), size: 10, pages: [][]byte{[]byte("x")}})
	}
	if _, ok := c.page("a", "s1", 1); ok {
		t.Fatal("the oldest preview of a session over its cap must go")
	}
	if _, ok := c.page(string(rune('a'+previewsPerSession+1)), "s1", 1); !ok {
		t.Fatal("newest preview missing")
	}
	c.put(&preview{id: "big", session: "s2", created: clk.Now(), size: previewBytes - 15, pages: [][]byte{[]byte("x")}})
	if c.size > previewBytes {
		t.Fatalf("size %d over budget", c.size)
	}
	clk.Add(previewTTL)
	if _, ok := c.page("big", "s2", 1); ok {
		t.Fatal("expired preview served")
	}
}
