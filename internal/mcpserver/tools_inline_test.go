// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/model"
)

var inlineIDRE = regexp.MustCompile(`^inline-[0-9a-f]{16}$`)

// inlineArgs is renderArgs with an inline template instead of template_id.
func inlineArgs(template any, extra map[string]any) map[string]any {
	a := renderArgs(nil)
	delete(a, "template_id")
	a["template"] = template
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func countInline(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.d.DB.R.QueryRow(`SELECT count(*) FROM templates WHERE id LIKE 'inline-%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Acceptances 1, 2 and 5 through MCP: render with template: {}, reuse,
// listing, reading and hiding of inline templates.
func TestRenderWithInlineTemplate(t *testing.T) {
	e, cs := docEnv(t, nil)
	var r RenderOut
	callOK(t, cs, "render_document", inlineArgs(map[string]any{}, nil), &r)
	if !inlineIDRE.MatchString(r.Template.ID) || !r.Template.Inline || r.Template.Version != 1 || r.Brand.ID != "acme" || len(r.Files) != 2 {
		t.Fatalf("render: %+v", r)
	}
	var r2 RenderOut
	callOK(t, cs, "render_document", inlineArgs(map[string]any{"name": "ignored"}, map[string]any{"markdown": "# Summary\n\nOther."}), &r2)
	if r2.Template.ID != r.Template.ID || r2.DocumentID == r.DocumentID || countInline(t, e) != 1 {
		t.Fatalf("same spec: %+v rows=%d", r2.Template, countInline(t, e))
	}
	var list ListDocumentsOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"markdown": "# Summary\n\nStored."}), nil)
	callOK(t, cs, "list_documents", map[string]any{"inline": true}, &list)
	if len(list.Documents) != 2 || !list.Documents[0].TemplateInline {
		t.Fatalf("inline filter: %+v", list)
	}
	var doc DocumentOut
	callOK(t, cs, "get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	if !doc.TemplateInline || !doc.Revision.Template.Inline || !doc.Revisions[0].Template.Inline {
		t.Fatalf("get_document: %+v", doc)
	}
	var gt GetTemplateOut
	callOK(t, cs, "get_template", map[string]any{"id": r.Template.ID}, &gt)
	var spec model.Template
	_ = json.Unmarshal(gt.Template, &spec)
	if !gt.Inline || gt.Archived || len(gt.Versions) != 1 || spec.Name != "Inline template" || spec.BrandID != "acme" {
		t.Fatalf("get_template: %+v %+v", gt, spec)
	}
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "folio://templates/" + r.Template.ID})
	if err != nil || !strings.Contains(res.Contents[0].Text, `"inline": true`) {
		t.Fatalf("resource: %v", err)
	}
	var lt ListTemplatesOut
	callOK(t, cs, "list_templates", map[string]any{"include_archived": true}, &lt)
	if len(lt.Templates) != 1 || lt.Templates[0].ID != "memo" {
		t.Fatalf("list_templates shows inline templates: %+v", lt)
	}
	for name, args := range map[string]map[string]any{
		"template and template_id":      inlineArgs(map[string]any{}, map[string]any{"template_id": "memo"}),
		"template and template_version": inlineArgs(map[string]any{}, map[string]any{"template_version": 1}),
		"no template":                   inlineArgs(nil, nil),
	} {
		if name == "no template" {
			delete(args, "template")
		}
		if te := callErr(t, cs, "render_document", args); te.Code != model.ErrInvalidInput {
			t.Errorf("%s: %+v", name, te)
		}
	}
	if te := callErr(t, cs, "list_documents", map[string]any{"inline": true, "template_id": "memo"}); te.Code != model.ErrInvalidInput {
		t.Fatalf("inline with template_id: %+v", te)
	}
	for name, args := range map[string]map[string]any{
		"update_template":  {"id": r.Template.ID, "expected_version": 1, "template": testTemplate(r.Template.ID, "acme")},
		"archive_template": {"id": r.Template.ID, "archived": true},
		"preview_template": {"id": r.Template.ID},
	} {
		if te := callErr(t, cs, name, args); te.Code != model.ErrNotFound {
			t.Errorf("%s on an inline id: %+v", name, te)
		}
	}
	if te := callErr(t, cs, "create_template", map[string]any{"template": testTemplate("inline-mine", "acme")}); te.Code != model.ErrInvalidInput {
		t.Fatalf("create_template with a reserved id: %+v", te)
	}
}

// Acceptances 3 and 8 through MCP: invalid or oversized inline specs fail
// before anything is written; validate_document reports /template findings.
func TestInlineTemplateErrors(t *testing.T) {
	e, cs := docEnv(t, nil)
	te := callErr(t, cs, "render_document", inlineArgs(map[string]any{"layout": map[string]any{"base": "memo"}}, nil))
	if te.Code != model.ErrValidationFailed || len(te.Details) == 0 || te.Details[0].Path != "/template/layout/base" {
		t.Fatalf("invalid spec: %+v", te)
	}
	var v ValidateOut
	callOK(t, cs, "validate_document", inlineArgs(map[string]any{"components": []string{"gantt"}}, nil), &v)
	if v.Valid || len(v.Findings) != 1 || v.Findings[0].Path != "/template/components/0" {
		t.Fatalf("validate invalid spec: %+v", v)
	}
	callOK(t, cs, "validate_document", inlineArgs(map[string]any{}, nil), &v)
	if !v.Valid || !v.Template.Inline || v.Brand.ID != "acme" {
		t.Fatalf("validate inline: %+v", v)
	}
	e.d.Catalog.MaxLayoutTypstBytes, e.d.Catalog.MaxInlineBytes = 32, 4096
	big := inlineArgs(map[string]any{"layout": map[string]any{"typst": strings.Repeat("x", 33)}}, nil)
	if te := callErr(t, cs, "render_document", big); te.Code != model.ErrTooLarge {
		t.Fatalf("large layout.typst: %+v", te)
	}
	huge := inlineArgs(map[string]any{"sections": []any{map[string]any{"title": strings.Repeat("y", 4096)}}}, nil)
	if te := callErr(t, cs, "render_document", huge); te.Code != model.ErrTooLarge {
		t.Fatalf("large spec: %+v", te)
	}
	e.r.calls.Store(0)
	if te := callErr(t, cs, "render_document", inlineArgs(map[string]any{}, map[string]any{"markdown": "FAIL"})); te.Code != model.ErrValidationFailed {
		t.Fatalf("render failure: %+v", te)
	}
	var docs int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM documents`).Scan(&docs)
	if n := countInline(t, e); n != 0 || docs != 0 {
		t.Fatalf("failed calls wrote %d inline rows and %d documents", n, docs)
	}
}

// Acceptance 6 through MCP (the PNG comparison runs in internal/e2e with
// the real pipeline): save_inline_template then render with the new id.
func TestSaveInlineTemplate(t *testing.T) {
	e, cs := docEnv(t, nil)
	var r RenderOut
	callOK(t, cs, "render_document", inlineArgs(map[string]any{"sections": []any{map[string]any{"title": "Summary", "required": true}}}, nil), &r)
	var out SaveInlineOut
	callOK(t, cs, "save_inline_template", map[string]any{"document_id": r.DocumentID, "id": "memo-plain", "name": "Plain memo", "category": "memo"}, &out)
	if out.ID != "memo-plain" || out.Version != 1 || out.SourceInlineID != r.Template.ID || len(out.Files) != 2 {
		t.Fatalf("save: %+v", out)
	}
	var again RenderOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"template_id": "memo-plain"}), &again)
	if again.Template.ID != "memo-plain" || again.Template.Inline || again.Template.Version != 1 {
		t.Fatalf("render with the saved template: %+v", again.Template)
	}
	var lt ListTemplatesOut
	callOK(t, cs, "list_templates", map[string]any{}, &lt)
	if len(lt.Templates) != 2 {
		t.Fatalf("the saved template must be listed: %+v", lt)
	}
	sample := map[string]any{"markdown": "# Summary\n\nHi.", "metadata": map[string]any{"title": "S", "date": "2026-10-19", "language": "en"}}
	var byID SaveInlineOut
	callOK(t, cs, "save_inline_template", map[string]any{"inline_id": r.Template.ID, "id": "memo-copy", "name": "Copy", "sample": sample}, &byID)
	if byID.SourceInlineID != r.Template.ID {
		t.Fatalf("save by inline id: %+v", byID)
	}
	var stored RenderOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"markdown": "# Summary\n\nStored doc."}), &stored)
	for name, c := range map[string]struct {
		args map[string]any
		want model.ErrorCode
	}{
		"both sources":         {map[string]any{"document_id": r.DocumentID, "inline_id": r.Template.ID, "id": "x1", "name": "X"}, model.ErrInvalidInput},
		"no source":            {map[string]any{"id": "x1", "name": "X"}, model.ErrInvalidInput},
		"inline_id, no sample": {map[string]any{"inline_id": r.Template.ID, "id": "x1", "name": "X"}, model.ErrInvalidInput},
		"stored document":      {map[string]any{"document_id": stored.DocumentID, "id": "x1", "name": "X"}, model.ErrInvalidInput},
		"reserved id":          {map[string]any{"document_id": r.DocumentID, "id": "inline-x1", "name": "X"}, model.ErrInvalidInput},
		"existing id":          {map[string]any{"document_id": r.DocumentID, "id": "memo", "name": "X"}, model.ErrConflict},
		"unknown inline id":    {map[string]any{"inline_id": "inline-ffffffffffffffff", "id": "x1", "name": "X", "sample": sample}, model.ErrNotFound},
		"unknown document":     {map[string]any{"document_id": "0199a000-0000-7000-8000-000000000001", "id": "x1", "name": "X"}, model.ErrNotFound},
	} {
		if te := callErr(t, cs, "save_inline_template", c.args); te.Code != c.want {
			t.Errorf("%s: %+v, want %s", name, te, c.want)
		}
	}
	var n int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'create_template' AND target_id = 'memo-plain' AND outcome = 'ok'`).Scan(&n)
	if n != 1 {
		t.Fatalf("create_template audit rows = %d", n)
	}
}

// Acceptance 7 (capabilities): default_brand follows the catalog rules.
func TestCapabilitiesDefaultBrand(t *testing.T) {
	e, cs := docEnv(t, nil)
	e.d.Config.MaxInlineTemplateBytes, e.d.Config.MaxLayoutTypstBytes = 256<<10, 64<<10
	var caps CapabilitiesOut
	callOK(t, cs, "get_capabilities", nil, &caps)
	if caps.DefaultBrand != "acme" || caps.Limits.MaxInlineTemplateBytes != 256<<10 || caps.Limits.MaxLayoutTypstBytes != 64<<10 {
		t.Fatalf("one brand: %q %+v", caps.DefaultBrand, caps.Limits)
	}
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("beta", "")}, nil)
	callOK(t, cs, "get_capabilities", nil, &caps)
	if caps.DefaultBrand != "" {
		t.Fatalf("two brands: %q", caps.DefaultBrand)
	}
	if te := callErr(t, cs, "render_document", inlineArgs(map[string]any{}, nil)); te.Code != model.ErrInvalidInput || !strings.Contains(te.Message, "acme, beta") {
		t.Fatalf("no default brand: %+v", te)
	}
	e.d.Catalog.DefaultBrandID = "beta"
	callOK(t, cs, "get_capabilities", nil, &caps)
	if caps.DefaultBrand != "beta" {
		t.Fatalf("configured: %q", caps.DefaultBrand)
	}
	e.d.Catalog.DefaultBrandID = "ghost"
	callOK(t, cs, "get_capabilities", nil, &caps)
	if caps.DefaultBrand != "" {
		t.Fatalf("ghost: %q", caps.DefaultBrand)
	}
	if te := callErr(t, cs, "render_document", inlineArgs(map[string]any{}, nil)); te.Code != model.ErrInvalidInput || !strings.Contains(te.Message, `"ghost"`) {
		t.Fatalf("ghost default brand: %+v", te)
	}
}

// Activity rows name the inline template of renders and validations and
// the source document of save_inline_template.
func TestInlineActivity(t *testing.T) {
	e, cs := docEnv(t, nil)
	rec := activity.NewRecorder(e.d.DB, nil, e.d.Logger, e.d.Now)
	e.d.Activity = rec
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	var r RenderOut
	callOK(t, cs, "render_document", inlineArgs(map[string]any{}, nil), &r)
	callOK(t, cs, "validate_document", inlineArgs(map[string]any{}, nil), nil)
	callOK(t, cs, "save_inline_template", map[string]any{"document_id": r.DocumentID, "id": "saved", "name": "Saved"}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for rec.Written() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("written %d rows, want 3", rec.Written())
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, _, err := activity.List(context.Background(), e.d.DB, activity.Filter{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %d %v", len(rows), err)
	}
	save, validate, render := rows[0], rows[1], rows[2]
	if render.TargetKind != "document" || render.TargetID != r.DocumentID || render.Detail.TemplateID != r.Template.ID {
		t.Errorf("render row %+v", render)
	}
	if validate.TargetKind != "template" || validate.TargetID != r.Template.ID {
		t.Errorf("validate row %+v", validate)
	}
	if save.Action != "save_inline_template" || save.TargetKind != "template" || save.TargetID != "saved" || save.Detail.SourceDocument != r.DocumentID {
		t.Errorf("save row %+v", save)
	}
}
