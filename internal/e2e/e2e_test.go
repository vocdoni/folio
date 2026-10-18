// SPDX-License-Identifier: AGPL-3.0-only

// Package e2e drives a complete in-process Folio (real store, sandboxed
// pandoc and typst, MCP over Streamable HTTP with bearer auth, public
// downloads) with the official go-sdk client.
package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/app"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/mcpserver"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/review"
	"github.com/vocdoni/folio/internal/sandbox"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

const (
	tokAlice = "alice-e2e-token-0123456789abcdefghijkl"
	tokBob   = "bob-e2e-token-0123456789abcdefghijklmno"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type harness struct {
	t    *testing.T
	app  *app.App
	url  string
	clk  *clock
	cs   *mcp.ClientSession
	logs *bytes.Buffer
	mu   sync.Mutex
	prog []string // progress messages received
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// start runs a Folio instance on an httptest server. env overrides FOLIO_*
// variables; rv is the AI reviewer (nil = not configured).
func start(t *testing.T, env map[string]string, rv review.Reviewer) *harness {
	t.Helper()
	for _, name := range []string{"pandoc", "typst"} {
		if _, err := sandbox.LookupTool(name); err != nil {
			t.Skipf("SKIPPING: %v (set FOLIO_PANDOC / FOLIO_TYPST)", err)
		}
	}
	srv := httptest.NewUnstartedServer(nil)
	h := &harness{t: t, url: "http://" + srv.Listener.Addr().String(), clk: &clock{t: time.Now().UTC().Truncate(time.Second)}, logs: &bytes.Buffer{}}
	dir := t.TempDir()
	t.Cleanup(func() { // the materialised runtime is read-only
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	vars := map[string]string{"FOLIO_PUBLIC_URL": h.url, "FOLIO_DATA_DIR": dir, "FOLIO_TOKENS": "alice:" + tokAlice + ",bob:" + tokBob,
		"FOLIO_LOG_FORMAT": "text", "FOLIO_RENDER_TIMEOUT": "60s"}
	for k, v := range env {
		vars[k] = v
	}
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := vars[k]; return v, ok }, config.ModeServe)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(context.Background(), cfg, app.Options{Version: "e2e", Logger: slog.New(slog.NewTextHandler(&syncWriter{w: h.logs, mu: &h.mu}, nil)),
		Reviewer: rv, JobRoot: t.TempDir(), Now: h.clk.Now, ProgressInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	h.app = a
	srv.Config.Handler = a.Handler
	srv.Start()
	t.Cleanup(func() { srv.Close(); a.Close() })
	h.cs = h.connect(tokAlice)
	return h
}

type syncWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (h *harness) connect(token string) *mcp.ClientSession {
	h.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			h.mu.Lock()
			h.prog = append(h.prog, r.Params.Message)
			h.mu.Unlock()
		}})
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool and decodes the JSON text block (exact numbers and
// bytes) into out. It returns the raw result.
func (h *harness) call(name string, args any, out any) *mcp.CallToolResult {
	h.t.Helper()
	params := &mcp.CallToolParams{Name: name, Arguments: args}
	params.SetProgressToken(name)
	res, err := h.cs.CallTool(context.Background(), params)
	if err != nil {
		h.t.Fatalf("%s: protocol error: %v", name, err)
	}
	if out != nil {
		raw := []byte(res.Content[1].(*mcp.TextContent).Text)
		if !json.Valid(raw) {
			raw, _ = json.Marshal(res.StructuredContent)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s: decode: %v", name, err)
		}
	}
	return res
}

func (h *harness) ok(name string, args any, out any) *mcp.CallToolResult {
	h.t.Helper()
	res := h.call(name, args, out)
	if res.IsError {
		h.t.Fatalf("%s failed: %s", name, res.Content[0].(*mcp.TextContent).Text)
	}
	return res
}

func (h *harness) fail(name string, args any) mcpserver.ToolError {
	h.t.Helper()
	var body struct {
		Error mcpserver.ToolError `json:"error"`
	}
	res := h.call(name, args, &body)
	if !res.IsError {
		h.t.Fatalf("%s: expected an error result", name)
	}
	return body.Error
}

func (h *harness) progress() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.prog)
}

func get(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func images(res *mcp.CallToolResult) int {
	n := 0
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			n++
		}
	}
	return n
}

func fileURL(files []mcpserver.FileOut, kind string) string {
	for _, f := range files {
		if f.Kind == kind {
			return f.URL
		}
	}
	return ""
}

const logoSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="60"><rect width="240" height="60" rx="8" fill="#0B7A75"/><circle cx="30" cy="30" r="18" fill="#FFFFFF"/></svg>`

func brandSpec(id, logo string) map[string]any {
	b := map[string]any{"id": id, "name": "E2E Studio",
		"colors": map[string]any{"primary": "#0B7A75", "secondary": "#14213D", "accent": "#FCA311", "text": "#111111", "muted": "#666666", "background": "#FFFFFF"},
		"fonts":  map[string]any{"heading": "Inter", "body": "Source Serif 4", "mono": "JetBrains Mono"},
		"header": map[string]any{"text": "E2E Studio", "show_logo": logo != ""},
		"footer": map[string]any{"text": "E2E Studio · e2e.test", "legal": "E2E Studio S.L.", "page_numbers": true},
		"docx":   map[string]any{"font_fallbacks": map[string]any{"heading": "Arial", "body": "Georgia"}}}
	if logo != "" {
		b["logo"] = map[string]any{"asset_id": logo, "width_mm": 30}
	}
	return b
}

// customLayout extends the proposal base layout (AC2b).
const customLayout = `#import "/base/proposal.typ": folio-layout as base-layout
#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = base-layout(
  meta: meta, brand: brand, labels: labels, options: options,
  { set par(justify: false); body },
)
`

func memoTemplate(id, brand string, typst any) map[string]any {
	return map[string]any{"id": id, "name": "Quote memo", "description": "Short quote with a budget.", "category": "quote", "brand_id": brand,
		"languages": []string{"en", "es"},
		"layout":    map[string]any{"base": "proposal", "typst": typst, "options": map[string]any{"cover": true, "toc": false, "page_size": "a4", "heading_numbering": false}},
		"data_schema": map[string]any{"type": "object", "required": []string{"budget"},
			"properties": map[string]any{"budget": map[string]any{"$ref": "folio:component/budget"}}},
		"sections":      []any{map[string]any{"title": "Summary", "required": true}, map[string]any{"title": "Budget", "required": true, "component": "budget"}},
		"components":    []string{"budget", "info"},
		"strict":        false,
		"body_skeleton": "# Summary\n\nTODO\n\n# Budget\n\n::: folio-budget\n:::\n",
		"sample":        memoInput("Sample quote", "1500"),
	}
}

func memoInput(title, price string) map[string]any {
	return map[string]any{
		"markdown": "# Summary\n\nWe will deliver the work described below.\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
		"metadata": map[string]any{"title": title, "date": "2026-10-18", "language": "en", "client": "ACME"},
		"data": map[string]any{"budget": map[string]any{"currency": "EUR", "tax_rate": "21",
			"items": []any{map[string]any{"description": "Design", "quantity": "2", "unit_price": price}}}},
	}
}

func renderArgs(templateID, title string, extra map[string]any) map[string]any {
	in := memoInput(title, "1500")
	in["template_id"] = templateID
	for k, v := range extra {
		in[k] = v
	}
	return in
}

// setupMemo creates the e2e brand and the memo template (no seeding needed).
func (h *harness) setupMemo() {
	h.t.Helper()
	h.ok("create_brand", map[string]any{"brand": brandSpec("e2e", "")}, nil)
	h.ok("create_template", map[string]any{"template": memoTemplate("memo", "e2e", nil)}, nil)
}

// TestAgentWorkflow covers AC2 (the core loop and downloads), AC3 (auth at
// the HTTP edge), AC10 (trash, purge and expiry at /f/, blob GC), AC17
// (idempotency_key) and AC14 (no reviewer → unavailable) on a seeded instance.
func TestAgentWorkflow(t *testing.T) {
	h := start(t, nil, nil)
	if !h.app.Seeded {
		t.Fatal("an empty database must be seeded")
	}

	// AC3: /mcp needs a valid token; /healthz, /robots.txt and /f/ do not.
	for _, auth := range []string{"", "Bearer " + strings.Repeat("z", 40)} {
		req, _ := http.NewRequest(http.MethodPost, h.url+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("/mcp with %q: %d", auth, resp.StatusCode)
		}
	}
	if code, _, body := get(t, h.url+"/healthz"); code != 200 || !strings.Contains(string(body), `"schema_version"`) {
		t.Fatalf("healthz: %d %s", code, body)
	}
	if code, _, body := get(t, h.url+"/robots.txt"); code != 200 || !strings.Contains(string(body), "Disallow: /") {
		t.Fatalf("robots: %d", code)
	}

	// AC2: list_templates → get_template → upload_asset → validate_document → render_document.
	var lt mcpserver.ListTemplatesOut
	h.ok("list_templates", map[string]any{}, &lt)
	if !slices.ContainsFunc(lt.Templates, func(s mcpserver.TemplateSummary) bool { return s.ID == "proposal" }) {
		t.Fatalf("starter templates: %+v", lt.Templates)
	}
	var gt mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "proposal"}, &gt)
	var tpl model.Template
	if err := json.Unmarshal(gt.Template, &tpl); err != nil || tpl.BodySkeleton == "" || tpl.Sample.Markdown == "" {
		t.Fatalf("template: %v", err)
	}
	var asset mcpserver.AssetOut
	h.ok("upload_asset", map[string]any{"name": "diagram.svg", "content_base64": base64.StdEncoding.EncodeToString([]byte(logoSVG))}, &asset)
	md := strings.Replace(tpl.Sample.Markdown, "::: folio-info", "![Diagram](asset:"+asset.AssetID+")\n\n::: folio-info", 1)
	input := map[string]any{"template_id": "proposal", "markdown": md, "metadata": tpl.Sample.Metadata, "data": tpl.Sample.Data}
	var v mcpserver.ValidateOut
	h.ok("validate_document", input, &v)
	if !v.Valid || len(v.Findings) != 0 {
		t.Fatalf("validate: %+v", v)
	}
	input["idempotency_key"] = "ac2-proposal"
	var r mcpserver.RenderOut
	res := h.ok("render_document", input, &r)
	if r.Revision != 1 || r.Unchanged || len(r.Files) != 2 || len(r.Previews) == 0 || images(res) != 1 {
		t.Fatalf("render: %+v (%d images)", r, images(res))
	}
	// Notification handlers run asynchronously: wait briefly for the last ones.
	steps := []string{render.StepParse, render.StepDOCX, render.StepPDF, render.StepChecks}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		prog := strings.Join(h.progress(), "|")
		missing := slices.DeleteFunc(slices.Clone(steps), func(s string) bool { return strings.Contains(prog, s) })
		if len(missing) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no progress notification for %q in %q", missing, prog)
		}
	}

	// AC17: a retry with the same idempotency_key returns the first document.
	var again mcpserver.RenderOut
	h.ok("render_document", input, &again)
	if again.DocumentID != r.DocumentID || !again.Unchanged || again.Revision != 1 {
		t.Fatalf("idempotent retry: %+v", again)
	}

	// get_document with page images, then downloads of PDF, DOCX and PNG.
	var doc mcpserver.DocumentOut
	res = h.ok("get_document", map[string]any{"id": r.DocumentID, "include_previews": "all"}, &doc)
	if doc.Revision.Pages < 2 || images(res) != min(doc.Revision.Pages, mcpserver.MaxImages) {
		t.Fatalf("get_document: %d pages, %d images", doc.Revision.Pages, images(res))
	}
	pdfURL, docxURL, pngURL := fileURL(r.Files, "pdf"), fileURL(r.Files, "docx"), r.Previews[0]
	for url, magic := range map[string]string{pdfURL: "%PDF", docxURL: "PK\x03\x04", pngURL: "\x89PNG"} {
		code, hdr, body := get(t, url)
		if code != 200 || !bytes.HasPrefix(body, []byte(magic)) || hdr.Get("X-Robots-Tag") != "noindex, nofollow" || hdr.Get("Cache-Control") != "private, max-age=300" {
			t.Fatalf("download %s: %d %q", url, code, hdr)
		}
	}

	// AC14 without a reviewer.
	if te := h.fail("review_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrUnavailable || te.Retryable {
		t.Fatalf("review without reviewer: %+v", te)
	}

	// publish → trash (404) → restore (200) → purge (404, blobs collected): AC2 + AC10.
	h.ok("publish_document", map[string]any{"id": r.DocumentID, "revision": 1}, nil)
	if te := h.fail("delete_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrInvalidInput {
		t.Fatalf("delete published without confirmation: %+v", te)
	}
	h.ok("delete_document", map[string]any{"id": r.DocumentID, "confirm_published": true}, nil)
	if code, _, body := get(t, pdfURL); code != http.StatusNotFound || strings.Contains(string(body), r.DocumentID) {
		t.Fatalf("trashed download: %d", code)
	}
	h.ok("restore_document", map[string]any{"id": r.DocumentID}, nil)
	if code, _, _ := get(t, pdfURL); code != 200 {
		t.Fatalf("restored download: %d", code)
	}
	pdfBlob := filepath.Join(h.app.Config.DataDir, "blobs", "sha256", r.Files[0].SHA256[:2], r.Files[0].SHA256[2:])
	if _, err := os.Stat(pdfBlob); err != nil {
		t.Fatalf("blob missing before purge: %v", err)
	}
	h.clk.Add(2 * time.Hour) // beyond FOLIO_BLOB_GC_GRACE
	h.ok("delete_document", map[string]any{"id": r.DocumentID, "purge": true, "confirm_published": true}, nil)
	for _, url := range []string{pdfURL, docxURL, pngURL} {
		if code, _, _ := get(t, url); code != http.StatusNotFound {
			t.Fatalf("purged download %s: %d", url, code)
		}
	}
	if _, err := os.Stat(pdfBlob); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purge did not collect the unreferenced blob: %v", err)
	}

	// AC10: template previews expire.
	var pv mcpserver.TemplateResultOut
	h.ok("preview_template", map[string]any{"id": "proposal", "include_previews": "none"}, &pv)
	if code, _, _ := get(t, fileURL(pv.Files, "pdf")); code != 200 {
		t.Fatalf("fresh preview: %d", code)
	}
	h.clk.Add(25 * time.Hour) // beyond FOLIO_PREVIEW_RETENTION
	if code, _, _ := get(t, fileURL(pv.Files, "pdf")); code != http.StatusNotFound {
		t.Fatalf("expired preview: %d", code)
	}

	// Audit actor is the token name; tokens and content never reach the logs.
	var n int
	if err := h.app.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE actor = 'alice' AND action = 'render_document'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit: %d %v", n, err)
	}
	h.mu.Lock()
	logs := h.logs.String()
	h.mu.Unlock()
	for _, secret := range []string{tokAlice, tokBob, "Website redesign", asset.AssetID + ")"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain %q", secret)
		}
	}
}

// TestTemplateAuthoring covers AC2b (brand and custom-layout template
// lifecycle, canonical round trip) and AC8 (optimistic concurrency).
func TestTemplateAuthoring(t *testing.T) {
	h := start(t, map[string]string{"FOLIO_SEED": "false"}, nil)
	var logo mcpserver.AssetOut
	h.ok("upload_asset", map[string]any{"name": "logo.svg", "content_base64": base64.StdEncoding.EncodeToString([]byte(logoSVG))}, &logo)
	h.ok("create_brand", map[string]any{"brand": brandSpec("studio", logo.AssetID)}, nil)
	var tr mcpserver.TemplateResultOut
	h.ok("create_template", map[string]any{"template": memoTemplate("studio-quote", "studio", customLayout), "note": "first"}, &tr)
	if tr.Version != 1 || len(tr.Warnings) != 0 {
		t.Fatalf("create_template: %+v", tr)
	}
	res := h.ok("preview_template", map[string]any{"id": "studio-quote"}, &tr)
	if images(res) != 1 || fileURL(tr.Files, "pdf") == "" {
		t.Fatalf("preview: %+v", tr)
	}
	var v1 mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "studio-quote"}, &v1)
	h.ok("update_template", map[string]any{"id": "studio-quote", "expected_version": 1, "template": v1.Template, "note": "same spec"}, &tr)
	var v2 mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "studio-quote"}, &v2)
	if v2.Version != 2 || len(v2.Versions) != 2 || v2.Versions[1].Note != "same spec" || v2.Versions[0].CreatedBy != "alice" {
		t.Fatalf("versions: %+v", v2)
	}
	c1, err1 := model.CanonicalBytes(v1.Template)
	c2, err2 := model.CanonicalBytes(v2.Template)
	if err1 != nil || err2 != nil || !bytes.Equal(c1, c2) || !bytes.Contains(c1, []byte(`"typst":"#import \"/base/proposal.typ\"`)) {
		t.Fatalf("spec did not round-trip as canonical JSON:\n%s\n%s", c1, c2)
	}

	h.ok("archive_template", map[string]any{"id": "studio-quote", "archived": true}, nil)
	if te := h.fail("render_document", renderArgs("studio-quote", "Q", nil)); te.Code != model.ErrArchived {
		t.Fatalf("render from archived template: %+v", te)
	}
	h.ok("archive_template", map[string]any{"id": "studio-quote", "archived": false}, nil)
	var v3 mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "studio-quote"}, &v3)
	if v3.Archived || v3.Version != 2 {
		t.Fatalf("unarchived: %+v", v3)
	}

	// AC8: concurrent updates with the same expected_version → exactly one wins.
	results := make([]bool, 4)
	codes := make([]model.ErrorCode, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cs := h.cs
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_template",
				Arguments: map[string]any{"id": "studio-quote", "expected_version": 2, "template": v3.Template}})
			if err != nil {
				return
			}
			results[i] = !res.IsError
			if res.IsError {
				var body struct{ Error mcpserver.ToolError }
				_ = json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &body)
				codes[i] = body.Error.Code
			}
		}(i)
	}
	wg.Wait()
	if wins := countTrue(results); wins != 1 {
		t.Fatalf("concurrent update_template: %d winners (codes %v)", wins, codes)
	}
	for i, c := range codes {
		if !results[i] && c != model.ErrConflict {
			t.Fatalf("loser %d got %q, want conflict", i, c)
		}
	}

	var r mcpserver.RenderOut
	h.ok("render_document", renderArgs("studio-quote", "Concurrent", nil), &r)
	results = make([]bool, 4)
	codes = make([]model.ErrorCode, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := renderArgs("studio-quote", "Concurrent", map[string]any{"document_id": r.DocumentID, "expected_revision": 1})
			args["data"] = memoInput("Concurrent", []string{"1000", "2000", "3000", "4000"}[i])["data"]
			res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "render_document", Arguments: args})
			if err != nil {
				return
			}
			results[i] = !res.IsError
			if res.IsError {
				var body struct{ Error mcpserver.ToolError }
				_ = json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &body)
				codes[i] = body.Error.Code
			}
		}(i)
	}
	wg.Wait()
	if wins := countTrue(results); wins != 1 {
		t.Fatalf("concurrent revisions: %d winners (codes %v)", wins, codes)
	}
	for i, c := range codes {
		if !results[i] && c != model.ErrConflict {
			t.Fatalf("revision loser %d got %q, want conflict", i, c)
		}
	}
}

func countTrue(bs []bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// TestConcurrencyAndBusy covers AC9 at the MCP level.
func TestConcurrencyAndBusy(t *testing.T) {
	h := start(t, map[string]string{"FOLIO_SEED": "false", "FOLIO_RENDER_CONCURRENCY": "2"}, nil)
	h.setupMemo()
	var wg sync.WaitGroup
	var okN, failN atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "render_document",
				Arguments: renderArgs("memo", "Parallel "+string(rune('A'+i)), nil)})
			if err == nil && !res.IsError {
				okN.Add(1)
			} else {
				failN.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if okN.Load() != 20 {
		t.Fatalf("20 renders at concurrency 2: %d ok, %d failed", okN.Load(), failN.Load())
	}

	b := start(t, map[string]string{"FOLIO_SEED": "false", "FOLIO_RENDER_CONCURRENCY": "1", "FOLIO_RENDER_QUEUE": "1"}, nil)
	b.setupMemo()
	var busy, done atomic.Int32
	var retryable atomic.Bool
	retryable.Store(true)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := b.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "render_document",
				Arguments: renderArgs("memo", "Burst "+string(rune('A'+i)), nil)})
			if err != nil {
				return
			}
			if !res.IsError {
				done.Add(1)
				return
			}
			var body struct{ Error mcpserver.ToolError }
			_ = json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &body)
			if body.Error.Code == model.ErrBusy {
				busy.Add(1)
				if !body.Error.Retryable {
					retryable.Store(false)
				}
			}
		}(i)
	}
	wg.Wait()
	if busy.Load() == 0 || done.Load() == 0 || !retryable.Load() {
		t.Fatalf("queue limit: %d busy, %d done, retryable=%v", busy.Load(), done.Load(), retryable.Load())
	}
}

type stubReviewer struct {
	mu   sync.Mutex
	fail bool
	seen []int
}

func (s *stubReviewer) Review(ctx context.Context, pages []review.Page, info review.Info) ([]review.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return nil, errors.New("upstream timeout")
	}
	s.seen = s.seen[:0]
	for _, p := range pages {
		if !bytes.HasPrefix(p.PNG, []byte("\x89PNG")) || info.Language != "en" {
			return nil, errors.New("bad page or info")
		}
		s.seen = append(s.seen, p.Number)
	}
	return []review.Finding{{Page: 1, Severity: "warning", Category: "typography", Message: "title wraps awkwardly"}}, nil
}

// TestReviewer covers AC14 with a stub Reviewer.
func TestReviewer(t *testing.T) {
	rv := &stubReviewer{}
	h := start(t, map[string]string{"FOLIO_SEED": "false"}, rv)
	h.setupMemo()
	var caps mcpserver.CapabilitiesOut
	h.ok("get_capabilities", nil, &caps)
	if !caps.ReviewerEnabled {
		t.Fatal("reviewer_enabled must be true")
	}
	var r mcpserver.RenderOut
	h.ok("render_document", renderArgs("memo", "Reviewed quote", nil), &r)
	var out mcpserver.ReviewOut
	h.ok("review_document", map[string]any{"id": r.DocumentID}, &out)
	if !out.Review.Advisory || len(out.Review.Findings) != 1 || len(rv.seen) == 0 || out.Review.ReviewedBy != "alice" {
		t.Fatalf("review: %+v seen %v", out, rv.seen)
	}
	var doc mcpserver.DocumentOut
	h.ok("get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	if !strings.Contains(string(doc.Revision.Review), `"advisory":true`) || doc.Status != "draft" || len(doc.Revision.Warnings) != len(r.Warnings) {
		t.Fatalf("stored review must be advisory and change nothing else: %+v", doc.Revision)
	}
	rv.mu.Lock()
	rv.fail = true
	rv.mu.Unlock()
	if te := h.fail("review_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrUnavailable {
		t.Fatalf("failing reviewer: %+v", te)
	}
	var r2 mcpserver.RenderOut
	h.ok("render_document", renderArgs("memo", "Still renders", nil), &r2) // reviewer failure never fails a render
	h.ok("get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	if !strings.Contains(string(doc.Revision.Review), "title wraps awkwardly") {
		t.Fatal("a failed review must keep the previous findings")
	}
}

// codeBlocks returns the fenced blocks of language lang that follow a
// heading starting with headingPrefix in a guide.
func codeBlocks(t *testing.T, topic, headingPrefix, lang string) []string {
	t.Helper()
	md, ok := mcpserver.Guide(topic)
	if !ok {
		t.Fatalf("guide %s missing", topic)
	}
	var out []string
	inSection, inBlock := false, false
	var cur strings.Builder
	for _, line := range strings.Split(md, "\n") {
		switch {
		case inBlock && line == "```":
			out = append(out, cur.String())
			cur.Reset()
			inBlock = false
		case inBlock:
			cur.WriteString(line + "\n")
		case strings.HasPrefix(line, "## "):
			inSection = strings.HasPrefix(line, headingPrefix)
		case inSection && line == "```"+lang:
			inBlock = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("guide %s has no %s block under %q", topic, lang, headingPrefix)
	}
	return out
}

// TestGuideExamples keeps the guides honest: their examples must validate
// and render on a real instance.
func TestGuideExamples(t *testing.T) {
	h := start(t, nil, nil)
	h.setupMemo()
	var gt mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "proposal"}, &gt)
	var tpl model.Template
	_ = json.Unmarshal(gt.Template, &tpl)

	md := codeBlocks(t, "markdown", "## A complete example", "markdown")[0]
	var v mcpserver.ValidateOut
	h.ok("validate_document", map[string]any{"template_id": "proposal", "markdown": md, "metadata": tpl.Sample.Metadata, "data": tpl.Sample.Data}, &v)
	if !v.Valid || len(v.Findings) != 0 {
		t.Fatalf("markdown guide example: %+v", v.Findings)
	}

	// Every construct the markdown guide lists as supported validates cleanly.
	probe := "# Summary {#summary}\n\nSee [the summary](#summary), [mail](mailto:a@example.com) and [web](https://example.com).\n\n" +
		"*it* **bold** ~~struck~~ H~2~O x^2^ `code` $E = mc^2$ \"quotes\" -- dash...[^n]\n\n[^n]: A note.\n\n" +
		"- [ ] open\n- [x] done\n  1. nested\n\n> quoted\n\n```\nfenced\n```\n\n$$a^2 + b^2 = c^2$$\n\n" +
		"| A | B |\n|---|--:|\n| 1 | 2 |\n\n+---+---+\n| a | b |\n+===+===+\n| 1 | 2 |\n+---+---+\n\n# Budget\n\n::: folio-budget\n:::\n"
	in := memoInput("Probe", "10")
	in["template_id"], in["markdown"] = "memo", probe
	h.ok("validate_document", in, &v)
	if !v.Valid || len(v.Findings) != 0 {
		t.Fatalf("supported syntax: %+v", v.Findings)
	}
	h.ok("render_document", in, nil)

	// The components guide's budget example validates.
	var budget map[string]any
	if err := json.Unmarshal([]byte(codeBlocks(t, "components", "## budget", "json")[0]), &budget); err != nil {
		t.Fatal(err)
	}
	in = memoInput("Budget example", "10")
	in["template_id"], in["data"] = "memo", budget
	h.ok("validate_document", in, &v)
	if !v.Valid || len(v.Findings) != 0 {
		t.Fatalf("budget example: valid=%v %+v", v.Valid, v.Findings)
	}

	// The typst-layouts examples compile into templates without warnings.
	for i, src := range codeBlocks(t, "typst-layouts", "## Example", "typst") {
		id := "guide-layout-" + string(rune('a'+i))
		var tr mcpserver.TemplateResultOut
		h.ok("create_template", map[string]any{"template": memoTemplate(id, "e2e", src)}, &tr)
		if len(tr.Warnings) != 0 {
			t.Fatalf("layout example %d: %+v", i, tr.Warnings)
		}
	}
}
