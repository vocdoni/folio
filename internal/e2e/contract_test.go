// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/mcpserver"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/validate"
)

// TestVerbatimContract covers A-AC2 at the MCP level on the seeded proposal:
// numbers in figure fields fail with the figure hint, component divs take no
// attributes, milestone deliverables must exist, and no tool returns computed
// values.
func TestVerbatimContract(t *testing.T) {
	h := start(t, nil, nil)
	var gt mcpserver.GetTemplateOut
	h.ok("get_template", map[string]any{"id": "proposal"}, &gt)
	var tpl model.Template
	if err := json.Unmarshal(gt.Template, &tpl); err != nil {
		t.Fatal(err)
	}
	if gt.Version != 1 || !strings.Contains(string(tpl.Sample.Data), `"€22,118.80"`) {
		t.Fatalf("starter proposal: version %d", gt.Version)
	}
	sample := func() map[string]any {
		var data map[string]any
		_ = json.Unmarshal(tpl.Sample.Data, &data)
		return map[string]any{"template_id": "proposal", "markdown": tpl.Sample.Markdown, "metadata": tpl.Sample.Metadata, "data": data}
	}
	findings := func(in map[string]any) []model.Finding {
		t.Helper()
		var v mcpserver.ValidateOut
		res := h.ok("validate_document", in, &v)
		raw, _ := json.Marshal(res.StructuredContent)
		if strings.Contains(string(raw), `"computed"`) {
			t.Fatalf("validate_document returned computed values: %s", raw)
		}
		return v.Findings
	}
	if fs := findings(sample()); len(fs) != 0 {
		t.Fatalf("starter sample: %+v", fs)
	}

	in := sample()
	budget := in["data"].(map[string]any)["budget"].(map[string]any)
	budget["items"].([]any)[0].(map[string]any)["amount"] = json.Number("4800")
	fs := findings(in)
	if len(fs) != 1 || fs[0].Code != model.CodeSchemaViolation || fs[0].Path != "/data/budget/items/0/amount" || fs[0].Hint != validate.FigureHint {
		t.Fatalf("number in a figure field: %+v", fs)
	}

	in = sample()
	in["markdown"] = strings.Replace(tpl.Sample.Markdown, "::: folio-milestones", "::: {.folio-milestones show_payments=true}", 1)
	fs = findings(in)
	if len(fs) != 1 || fs[0].Code != model.CodeComponentAttribute {
		t.Fatalf("component attribute: %+v", fs)
	}

	in = sample()
	ms := in["data"].(map[string]any)["milestones"].([]any)
	ms[2].(map[string]any)["deliverables"] = []any{"D3", "D9"}
	fs = findings(in)
	if len(fs) != 1 || fs[0].Code != model.CodeUnknownDeliverable || fs[0].Path != "/data/milestones/2/deliverables/1" {
		t.Fatalf("unknown deliverable: %+v", fs)
	}
}
