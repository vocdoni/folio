// SPDX-License-Identifier: AGPL-3.0-only

// Package skill generates the Agent Skill in skills/folio from the MCP guides,
// so get_guide and the skill share one source of truth. SKILL.md comes from
// skill.md.tmpl plus a generated reference list; references/<topic>.md are
// the guides verbatim behind a generated-file banner.
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
// path relative to Dir. The output is deterministic.
func Files() (map[string][]byte, error) {
	files := map[string][]byte{}
	var refs strings.Builder
	for _, topic := range mcpserver.Topics {
		md, ok := mcpserver.Guide(topic)
		if !ok {
			return nil, fmt.Errorf("skill: guide %q missing", topic)
		}
		title, summary := head(md)
		if title == "" || summary == "" {
			return nil, fmt.Errorf("skill: guide %q needs a '# Title' line and a summary line", topic)
		}
		fmt.Fprintf(&refs, "- [references/%s.md](references/%s.md): %s. %s\n", topic, topic, title, summary)
		files["references/"+topic+".md"] = []byte(banner(topic) + md)
	}
	if !strings.Contains(skillTemplate, "{{REFERENCES}}") {
		return nil, fmt.Errorf("skill: skill.md.tmpl lacks the {{REFERENCES}} placeholder")
	}
	files["SKILL.md"] = []byte(strings.Replace(skillTemplate, "{{REFERENCES}}\n", refs.String(), 1))
	return files, nil
}

func banner(topic string) string {
	return "<!-- Generated from internal/mcpserver/guides/" + topic + ".md by `go generate ./internal/skill`. Do not edit. -->\n\n"
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
