// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// retiredPhrases describe the money-computing contract Folio no longer has.
var retiredPhrases = []string{"computes every money value", "computed.budget", "computed.milestones", "expected_total",
	"show_payments", "payment_pct", "never compute", "Folio computes", "exact decimal"}

// requiredPhrase states the contract in every place agents and people read.
const requiredPhrase = "send every value exactly as it should appear"

// readmeWithoutUpgrading returns the README without its "Upgrading from the
// money-computing contract" section, which names the retired fields on purpose.
func readmeWithoutUpgrading(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "\n## Upgrading from the money-computing contract\n")
	if start < 0 {
		t.Fatal("README lacks the upgrading section")
	}
	end := strings.Index(s[start+1:], "\n## ")
	if end < 0 {
		t.Fatal("the upgrading section must be followed by another section")
	}
	return s[:start] + s[start+1+end:]
}

// TestContentNeutralWording (A-AC3): README, guides, the skill template,
// the instructions and the prompts never claim that Folio computes figures,
// and state that values are sent exactly as they should appear.
func TestContentNeutralWording(t *testing.T) {
	texts := map[string]string{"README": readmeWithoutUpgrading(t), "instructions": Instructions}
	for _, topic := range Topics {
		texts["guide "+topic] = guide(t, topic)
	}
	tmpl, err := os.ReadFile(filepath.Join("..", "skill", "skill.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	texts["skill template"] = string(tmpl)
	_, cs := docEnv(t, nil)
	prompts := map[string]*mcp.GetPromptParams{
		"prompt new_document":               {Name: "new_document", Arguments: map[string]string{"template_id": "memo"}},
		"prompt new_document (any)":         {Name: "new_document"},
		"prompt new_template":               {Name: "new_template", Arguments: map[string]string{"category": "quote"}},
		"prompt new_template (no category)": {Name: "new_template"},
	}
	for name, params := range prompts {
		res, err := cs.GetPrompt(context.Background(), params)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		texts[name] = res.Messages[0].Content.(*mcp.TextContent).Text
	}
	for name, text := range texts {
		for _, p := range retiredPhrases {
			if strings.Contains(text, p) {
				t.Errorf("%s still says %q", name, p)
			}
		}
	}
	for _, name := range []string{"README", "instructions", "skill template", "guide components", "guide workflow", "guide recipes"} {
		if !strings.Contains(texts[name], requiredPhrase) {
			t.Errorf("%s does not say %q", name, requiredPhrase)
		}
	}
	if !strings.Contains(texts["prompt new_document"], "exactly as it should appear") {
		t.Error("the new_document prompt does not state the verbatim contract")
	}
}
