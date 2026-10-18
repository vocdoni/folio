// SPDX-License-Identifier: AGPL-3.0-only

// Package skill generates the Agent Skill in skills/folio from the MCP guides,
// so get_guide and the skill share one source of truth. The skill is a single
// self-contained SKILL.md: skill.md.tmpl plus a generated "Deeper guides"
// list that tells the agent to call get_guide for each topic.
package skill

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/vocdoni/folio/internal/mcpserver"
)

//go:generate go run ./gen -out ../../skills/folio

//go:embed skill.md.tmpl
var skillTemplate string

// Dir is the skill directory relative to the repository root.
const Dir = "skills/folio"

// Files returns every file of the skill directory keyed by its slash-separated
// path relative to Dir (only SKILL.md). The output is deterministic.
func Files() (map[string][]byte, error) {
	var guides strings.Builder
	for _, topic := range mcpserver.Topics {
		md, ok := mcpserver.Guide(topic)
		if !ok {
			return nil, fmt.Errorf("skill: guide %q missing", topic)
		}
		title, summary := head(md)
		if title == "" || summary == "" {
			return nil, fmt.Errorf("skill: guide %q needs a '# Title' line and a summary line", topic)
		}
		fmt.Fprintf(&guides, "- `%s`: %s. %s\n", topic, title, summary)
	}
	if !strings.Contains(skillTemplate, "{{GUIDES}}") {
		return nil, fmt.Errorf("skill: skill.md.tmpl lacks the {{GUIDES}} placeholder")
	}
	return map[string][]byte{"SKILL.md": []byte(strings.Replace(skillTemplate, "{{GUIDES}}\n", guides.String(), 1))}, nil
}

// head returns a guide's "# " title and the first non-empty line after it.
func head(md string) (title, summary string) {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case title == "" && strings.HasPrefix(line, "# "):
			title = strings.TrimPrefix(line, "# ")
		case title != "":
			return title, line
		}
	}
	return title, ""
}
