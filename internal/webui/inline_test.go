// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
)

// Documents rendered with an inline template through the UI API: flagged in
// the list and the detail, filterable, revisable with their inline id, and
// savable as a stored template; inline templates are never listed or edited.
func TestInlineTemplatesInTheUI(t *testing.T) {
	sv := newServices(t, nil)
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	decode[TemplateSavedOut](t, sv.do("POST", "/ui/api/templates", TemplateIn{Template: templateSpec("memo", "acme")}, nil), 200)
	meta := json.RawMessage(`{"title":"Inline memo","date":"2026-10-19","language":"en"}`)
	inl, err := sv.opts.Documents.Render(context.Background(), "agent", documents.RenderRequest{Template: &model.Template{},
		Input: model.DocumentInput{Markdown: "# Summary\n\nFrom an agent.", Metadata: meta}})
	if err != nil {
		t.Fatal(err)
	}
	stored := decode[RenderOut](t, sv.do("POST", "/ui/api/render", RenderIn{TemplateID: "memo", Markdown: "# Summary\n\nStored.",
		Metadata: meta, IdempotencyKey: "6f1c2d3e-0000-4000-8000-0000000000aa"}, nil), 200)

	list := decode[DocumentListOut](t, sv.do("GET", "/ui/api/documents", nil, nil), 200)
	if len(list.Items) != 2 || !list.Items[1].TemplateInline || list.Items[0].TemplateInline {
		t.Fatalf("list %+v", list.Items)
	}
	only := decode[DocumentListOut](t, sv.do("GET", "/ui/api/documents?inline=true", nil, nil), 200)
	if len(only.Items) != 1 || only.Items[0].ID != inl.DocumentID {
		t.Fatalf("inline filter %+v", only.Items)
	}
	if w := sv.do("GET", "/ui/api/documents?inline=true&template=memo", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("inline with template: %d", w.Code)
	}
	doc := decode[DocumentOut](t, sv.do("GET", "/ui/api/documents/"+inl.DocumentID, nil, nil), 200)
	if !doc.TemplateInline || !doc.Revision.Template.Inline || !doc.Revisions[0].Template.Inline {
		t.Fatalf("doc %+v", doc)
	}
	tpl := decode[ItemOut](t, sv.do("GET", "/ui/api/templates/"+inl.Template.ID, nil, nil), 200)
	if !tpl.Inline || !strings.Contains(string(tpl.Spec), `"name":"Inline template"`) {
		t.Fatalf("inline template item %+v", tpl)
	}
	if l := decode[TemplateListOut](t, sv.do("GET", "/ui/api/templates?include_archived=true", nil, nil), 200); len(l.Items) != 1 {
		t.Fatalf("templates list shows inline templates: %+v", l.Items)
	}
	src := decode[SourceOut](t, sv.do("GET", "/ui/api/documents/"+inl.DocumentID+"/source", nil, nil), 200)
	if src.TemplateID != inl.Template.ID {
		t.Fatalf("source %+v", src)
	}
	one := 1
	rev := decode[RenderOut](t, sv.do("POST", "/ui/api/render", RenderIn{TemplateID: src.TemplateID, Markdown: "# Summary\n\nRevised in the UI.",
		Metadata: meta, DocumentID: inl.DocumentID, ExpectedRevision: &one}, nil), 200)
	if rev.Revision != 2 || rev.Template.ID != inl.Template.ID || !rev.Template.Inline {
		t.Fatalf("revise %+v", rev)
	}

	saved := decode[SaveTemplateOut](t, sv.do("POST", "/ui/api/documents/"+inl.DocumentID+"/save-template",
		SaveTemplateIn{ID: "agent-memo", Name: "Agent memo", Category: "memo"}, nil), 200)
	if saved.ID != "agent-memo" || saved.Version != 1 || saved.SourceInlineID != inl.Template.ID || len(saved.Files) == 0 {
		t.Fatalf("save %+v", saved)
	}
	if l := decode[TemplateListOut](t, sv.do("GET", "/ui/api/templates", nil, nil), 200); len(l.Items) != 2 {
		t.Fatalf("the saved template is not listed: %+v", l.Items)
	}
	for name, c := range map[string]struct {
		path string
		in   SaveTemplateIn
		code int
	}{
		"existing id":     {"/ui/api/documents/" + inl.DocumentID + "/save-template", SaveTemplateIn{ID: "agent-memo", Name: "X"}, http.StatusConflict},
		"reserved id":     {"/ui/api/documents/" + inl.DocumentID + "/save-template", SaveTemplateIn{ID: "inline-mine", Name: "X"}, http.StatusBadRequest},
		"stored document": {"/ui/api/documents/" + stored.DocumentID + "/save-template", SaveTemplateIn{ID: "x2", Name: "X"}, http.StatusBadRequest},
	} {
		if w := sv.do("POST", c.path, c.in, nil); w.Code != c.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := sv.do("POST", "/ui/api/templates", TemplateIn{Template: templateSpec("inline-x", "acme")}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("reserved id in the editor: %d", w.Code)
	}
	edit := templateSpec(inl.Template.ID, "acme")
	if w := sv.do("PUT", "/ui/api/templates/"+inl.Template.ID, TemplateIn{Template: edit, ExpectedVersion: 1}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("update inline: %d", w.Code)
	}
	if w := sv.do("POST", "/ui/api/templates/"+inl.Template.ID+"/archive", ArchiveIn{Archived: true}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("archive inline: %d", w.Code)
	}
}
