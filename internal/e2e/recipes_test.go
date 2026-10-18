// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"encoding/json"
	"testing"

	"github.com/vocdoni/folio/internal/mcpserver"
)

// TestRecipeExamples keeps the recipes guide honest: its quote example
// validates against the seeded quote template and renders without warnings.
func TestRecipeExamples(t *testing.T) {
	h := start(t, nil, nil)
	var args map[string]any
	if err := json.Unmarshal([]byte(codeBlocks(t, "recipes", "## Quote", "json")[0]), &args); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{}
	for _, k := range []string{"template_id", "markdown", "metadata", "data"} {
		in[k] = args[k]
	}
	var v mcpserver.ValidateOut
	h.ok("validate_document", in, &v)
	if !v.Valid || len(v.Findings) != 0 {
		t.Fatalf("quote recipe: valid=%v findings=%+v", v.Valid, v.Findings)
	}
	var r mcpserver.RenderOut
	h.ok("render_document", args, &r)
	if len(r.Warnings) != 0 || len(r.Files) != 2 {
		t.Fatalf("quote recipe render: %+v", r)
	}
}
