// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/mcpserver"
)

// TestInlineTemplates covers inline acceptance 1 (template: {} renders a PDF
// and a DOCX with the default brand and the report layout) and 6 (saving the
// inline template and rendering the same input with the stored copy gives
// the same first page) with the real pipeline on a seeded instance.
func TestInlineTemplates(t *testing.T) {
	h := start(t, nil, nil)
	input := func(extra map[string]any) map[string]any {
		a := map[string]any{"markdown": "# Introduction\n\nA one-off note rendered without a stored template.\n\n# Next steps\n\nNone for now.",
			"metadata": map[string]any{"title": "Inline note", "date": "2026-10-19", "language": "en", "client": "ACME"}}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}
	var r mcpserver.RenderOut
	h.ok("render_document", input(map[string]any{"template": map[string]any{}}), &r)
	if !r.Template.Inline || !strings.HasPrefix(r.Template.ID, "inline-") || r.Brand.ID != "example" || len(r.Previews) == 0 {
		t.Fatalf("inline render: %+v", r)
	}
	for kind, magic := range map[string]string{"pdf": "%PDF-", "docx": "PK"} {
		status, _, body := get(t, fileURL(r.Files, kind))
		if status != 200 || !bytes.HasPrefix(body, []byte(magic)) {
			t.Fatalf("%s download: %d", kind, status)
		}
	}
	var gt mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": r.Template.ID}, &gt)
	var spec struct {
		BrandID string `json:"brand_id"`
		Layout  struct {
			Base string `json:"base"`
		} `json:"layout"`
	}
	if err := json.Unmarshal(gt.Template, &spec); err != nil || !gt.Inline || spec.Layout.Base != "report" || spec.BrandID != "example" {
		t.Fatalf("inline template: %+v %s", gt, gt.Template)
	}

	var saved mcpserver.SaveInlineOut
	h.ok("save_inline_template", map[string]any{"document_id": r.DocumentID, "id": "note", "name": "Note"}, &saved)
	if saved.SourceInlineID != r.Template.ID || saved.Version != 1 {
		t.Fatalf("save: %+v", saved)
	}
	var again mcpserver.RenderOut
	h.ok("render_document", input(map[string]any{"template_id": "note"}), &again)
	if again.Template.ID != "note" || again.Template.Inline || len(again.Previews) == 0 {
		t.Fatalf("render with the saved template: %+v", again)
	}
	_, _, first := get(t, r.Previews[0])
	_, _, second := get(t, again.Previews[0])
	if len(first) == 0 || !bytes.Equal(first, second) {
		t.Fatalf("first pages differ (%d vs %d bytes)", len(first), len(second))
	}
}
