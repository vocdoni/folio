// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecipesGuideCoversTheJobs(t *testing.T) {
	requireAll(t, "recipes", []string{
		"## Proposal", "## Quote", "## Statement of work", "## Revise a colleague's document", "## Create a brand and a template",
		"`list_templates", "`get_template", "`validate_document`", "`render_document`", "`get_document", "`publish_document",
		"`upload_asset`", "`create_brand`", "`create_template`", "`preview_template", "`update_template", "expected_revision",
		"idempotency_key", "computed.budget.total",
	})
	md := guide(t, "recipes")
	blocks := strings.Split(md, "```json\n")
	if len(blocks) != 2 {
		t.Fatalf("recipes: want exactly one json example, got %d", len(blocks)-1)
	}
	body, _, ok := strings.Cut(blocks[1], "\n```")
	if !ok || !json.Valid([]byte(body)) {
		t.Fatal("recipes: the json example must be valid JSON")
	}
}
