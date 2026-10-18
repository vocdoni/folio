// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/model"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="60"><rect width="200" height="60" fill="#1F4FD8"/></svg>`

func testBrand(id, logo string) map[string]any {
	b := map[string]any{
		"id": id, "name": "Acme",
		"colors": map[string]any{"primary": "#1F4FD8", "secondary": "#0B1F44", "accent": "#F2A900", "text": "#1A1A1A", "muted": "#6B7280", "background": "#FFFFFF"},
		"fonts":  map[string]any{"heading": "Inter", "body": "Source Serif 4", "mono": "JetBrains Mono"},
		"header": map[string]any{"text": "Acme", "show_logo": logo != ""},
		"footer": map[string]any{"text": "Acme · acme.test", "legal": "", "page_numbers": true},
		"docx":   map[string]any{},
	}
	if logo != "" {
		b["logo"] = map[string]any{"asset_id": logo, "width_mm": 30}
	}
	return b
}

func testTemplate(id, brand string) map[string]any {
	return map[string]any{
		"id": id, "name": "Memo", "description": "Short internal memo.", "category": "memo", "brand_id": brand,
		"languages":  []string{"en", "es"},
		"layout":     map[string]any{"base": "report", "typst": nil, "options": map[string]any{"cover": false, "toc": false, "page_size": "a4", "heading_numbering": false}},
		"sections":   []any{map[string]any{"title": "Summary", "required": true, "aliases": []string{"Resumen"}}},
		"components": []string{}, "strict": false,
		"body_skeleton": "# Summary\n\n",
		"sample": map[string]any{"markdown": "# Summary\n\nAll good.",
			"metadata": map[string]any{"title": "Memo", "date": "2026-10-18", "language": "en"}},
	}
}

func uploadLogo(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	var a AssetOut
	callOK(t, cs, "upload_asset", map[string]any{"name": "logo.svg", "content_base64": base64.StdEncoding.EncodeToString([]byte(testSVG))}, &a)
	if a.AssetID == "" || a.MIME != "image/svg+xml" {
		t.Fatalf("upload: %+v", a)
	}
	return a.AssetID
}

func TestGuideAndCapabilities(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	var g GuideOut
	res := callOK(t, cs, "get_guide", map[string]any{}, &g)
	for _, topic := range Topics {
		if !strings.Contains(g.Markdown, "`"+topic+"`") {
			t.Errorf("index lacks %s", topic)
		}
	}
	if txt := res.Content[1].(*mcp.TextContent).Text; txt != g.Markdown {
		t.Error("the guide text block must be the Markdown itself")
	}
	callOK(t, cs, "get_guide", map[string]any{"topic": "components"}, &g)
	if g.Topic != "components" || !strings.HasPrefix(g.Markdown, "# ") {
		t.Errorf("components guide: %+v", g.Topic)
	}
	if te := callErr(t, cs, "get_guide", map[string]any{"topic": "nope"}); te.Code != model.ErrInvalidInput || !strings.Contains(te.Message, "typst-layouts") {
		t.Errorf("unknown topic: %+v", te)
	}
	var caps CapabilitiesOut
	callOK(t, cs, "get_capabilities", nil, &caps)
	if len(caps.BaseLayouts) != 4 || len(caps.Components) != 5 || caps.SandboxLevel != "rlimits-only" || caps.ReviewerEnabled ||
		caps.Limits.MaxListLimit != 200 || caps.Limits.MaxImagesPerCall != 6 || len(caps.Fonts) == 0 || caps.TypstPackages == nil {
		t.Fatalf("capabilities: %+v", caps)
	}
	if !strings.Contains(string(caps.Components[0].Schema), `"items"`) || !strings.Contains(string(caps.BaseLayouts[0].OptionsSchema), "page_size") {
		t.Errorf("schemas missing: %s / %s", caps.Components[0].Schema, caps.BaseLayouts[0].OptionsSchema)
	}
}

func TestBrandTools(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	logo := uploadLogo(t, cs)
	var v VersionOut
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", logo), "note": "first"}, &v)
	if v != (VersionOut{ID: "acme", Version: 1}) {
		t.Fatalf("create: %+v", v)
	}
	bad := testBrand("bad", "")
	bad["colors"].(map[string]any)["primary"] = "blue"
	te := callErr(t, cs, "create_brand", map[string]any{"brand": bad})
	if te.Code != model.ErrValidationFailed || len(te.Details) != 1 || te.Details[0].Path != "/colors/primary" {
		t.Fatalf("invalid brand: %+v", te)
	}
	if te := callErr(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}); te.Code != model.ErrConflict {
		t.Fatalf("duplicate: %+v", te)
	}
	callOK(t, cs, "update_brand", map[string]any{"id": "acme", "expected_version": 1, "brand": testBrand("acme", "")}, &v)
	if v.Version != 2 {
		t.Fatalf("update: %+v", v)
	}
	if te := callErr(t, cs, "update_brand", map[string]any{"id": "acme", "expected_version": 1, "brand": testBrand("acme", "")}); te.Code != model.ErrConflict || !strings.Contains(te.Message, "expected_version=2") {
		t.Fatalf("stale update: %+v", te)
	}
	var gb GetBrandOut
	callOK(t, cs, "get_brand", map[string]any{"id": "acme", "version": 1}, &gb)
	if gb.Version != 1 || gb.LatestVersion != 2 || len(gb.Versions) != 2 || gb.Versions[0].Note != "first" || !strings.Contains(string(gb.Brand), logo) {
		t.Fatalf("get_brand: %+v", gb)
	}
	callOK(t, cs, "archive_brand", map[string]any{"id": "acme", "archived": true}, nil)
	var lb ListBrandsOut
	callOK(t, cs, "list_brands", map[string]any{}, &lb)
	if len(lb.Brands) != 0 {
		t.Fatalf("archived brand listed: %+v", lb)
	}
	callOK(t, cs, "list_brands", map[string]any{"include_archived": true}, &lb)
	if len(lb.Brands) != 1 || !lb.Brands[0].Archived || lb.Brands[0].Name != "Acme" {
		t.Fatalf("include_archived: %+v", lb)
	}
	if te := callErr(t, cs, "list_brands", map[string]any{"limit": 500}); te.Code != model.ErrInvalidInput {
		t.Fatalf("limit: %+v", te)
	}
	var n int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE actor = ? AND action IN ('create_brand','update_brand','archive_brand')`, LocalActor).Scan(&n)
	if n != 6 { // 3 successes (service) + 3 failures (create invalid, create duplicate, stale update)
		t.Fatalf("audit rows = %d, want 6", n)
	}
}

func TestTemplateTools(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}, nil)
	var tr TemplateResultOut
	res := callOK(t, cs, "create_template", map[string]any{"template": testTemplate("memo", "acme"), "note": "v1"}, &tr)
	if tr.Version != 1 || len(tr.Files) != 2 || len(tr.Previews) != 1 || len(tr.Warnings) != 0 {
		t.Fatalf("create_template: %+v", tr)
	}
	for _, f := range tr.Files {
		if !strings.HasPrefix(f.URL, "https://folio.example.test/f/") || !strings.HasSuffix(f.URL, "/"+f.Name) || f.ExpiresAt.IsZero() {
			t.Errorf("file %+v", f)
		}
		if f.Kind == "pdf" && f.Pages != 2 {
			t.Errorf("pdf pages = %d", f.Pages)
		}
	}
	links := 0
	for _, ct := range res.Content {
		if rl, ok := ct.(*mcp.ResourceLink); ok && strings.HasPrefix(rl.URI, "https://folio.example.test/f/") {
			links++
		}
	}
	if links != 2 {
		t.Errorf("resource links = %d, want 2", links)
	}
	var gt GetTemplateOut
	callOK(t, cs, "get_template", map[string]any{"id": "memo"}, &gt)
	if gt.Version != 1 || len(gt.Versions) != 1 || gt.Versions[0].Note != "v1" {
		t.Fatalf("get_template: %+v", gt)
	}
	var spec map[string]any
	_ = json.Unmarshal(gt.Template, &spec)
	callOK(t, cs, "update_template", map[string]any{"id": "memo", "expected_version": 1, "template": spec}, &tr)
	if tr.Version != 2 {
		t.Fatalf("update: %+v", tr)
	}
	if te := callErr(t, cs, "update_template", map[string]any{"id": "memo", "expected_version": 1, "template": spec}); te.Code != model.ErrConflict {
		t.Fatalf("stale update: %+v", te)
	}
	broken := testTemplate("broken", "acme")
	broken["components"] = []string{"gantt"}
	if te := callErr(t, cs, "create_template", map[string]any{"template": broken}); te.Code != model.ErrValidationFailed || te.Details[0].Path != "/components/0" {
		t.Fatalf("invalid template: %+v", te)
	}
	if te := callErr(t, cs, "create_template", map[string]any{"template": map[string]any{"id": "x", "sectons": []any{}}}); te.Code != model.ErrInvalidInput {
		t.Fatalf("misspelt field must be rejected by the schema: %+v", te)
	}

	res = callOK(t, cs, "preview_template", map[string]any{"id": "memo", "include_previews": "all"}, &tr)
	var widths []int
	for _, ct := range res.Content {
		if img, ok := ct.(*mcp.ImageContent); ok {
			cfg, err := png.DecodeConfig(bytes.NewReader(img.Data))
			if err != nil {
				t.Fatal(err)
			}
			widths = append(widths, cfg.Width)
		}
	}
	if len(widths) != 2 || widths[0] != MaxImageWidth || widths[1] != MaxImageWidth {
		t.Fatalf("preview images: %v", widths)
	}
	if e.raster.calls.Load() != 1 {
		t.Fatalf("page 2 is not stored (PreviewPagesStored=1) and must be rasterised once; calls = %d", e.raster.calls.Load())
	}
	if te := callErr(t, cs, "preview_template", map[string]any{"id": "memo", "include_previews": "some"}); te.Code != model.ErrInvalidInput {
		t.Fatalf("bad include_previews: %+v", te)
	}
	callOK(t, cs, "archive_template", map[string]any{"id": "memo", "archived": true}, nil)
	var lt ListTemplatesOut
	callOK(t, cs, "list_templates", map[string]any{}, &lt)
	if len(lt.Templates) != 0 {
		t.Fatalf("archived template listed: %+v", lt)
	}
	callOK(t, cs, "list_templates", map[string]any{"include_archived": true, "category": "memo"}, &lt)
	if len(lt.Templates) != 1 || lt.Templates[0].BrandID != "acme" || lt.Templates[0].Version != 2 || len(lt.Templates[0].Languages) != 2 {
		t.Fatalf("list_templates: %+v", lt)
	}
	var n int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'preview_template' AND outcome = 'ok'`).Scan(&n)
	if n != 1 {
		t.Fatalf("preview audit rows = %d", n)
	}
}

func TestAssetTools(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	id := uploadLogo(t, cs)
	if te := callErr(t, cs, "upload_asset", map[string]any{"name": "x.png", "content_base64": "AAAA", "url": "https://example.com/x.png"}); te.Code != model.ErrInvalidInput {
		t.Fatalf("both sources: %+v", te)
	}
	evil := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	if te := callErr(t, cs, "upload_asset", map[string]any{"name": "x.svg", "content_base64": base64.StdEncoding.EncodeToString([]byte(evil))}); te.Code != model.ErrInvalidInput {
		t.Fatalf("script svg: %+v", te)
	}
	var la ListAssetsOut
	callOK(t, cs, "list_assets", map[string]any{"query": "LOGO"}, &la)
	if len(la.Assets) != 1 || la.Assets[0].AssetID != id {
		t.Fatalf("list_assets: %+v", la)
	}
	callOK(t, cs, "delete_asset", map[string]any{"id": id}, nil)
	callOK(t, cs, "list_assets", map[string]any{}, &la)
	if len(la.Assets) != 0 {
		t.Fatalf("deleted asset listed: %+v", la)
	}
	if te := callErr(t, cs, "delete_asset", map[string]any{"id": id}); te.Code != model.ErrNotFound {
		t.Fatalf("second delete: %+v", te)
	}
	tools, _ := cs.ListTools(t.Context(), nil)
	for _, tl := range tools.Tools {
		if tl.Name == "delete_asset" && !*tl.Annotations.DestructiveHint {
			t.Error("delete_asset must be destructive")
		}
		if tl.Name == "upload_asset" && !*tl.Annotations.OpenWorldHint {
			t.Error("upload_asset fetches URLs: openWorldHint")
		}
	}
}
