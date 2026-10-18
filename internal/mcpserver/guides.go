// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"embed"
	"fmt"
	"strings"
)

// The guides are the single source of truth for agent documentation: served
// by get_guide and folio://guide/{topic}, and the source of the Agent Skill
// references.
//
//go:embed guides/*.md
var guideFS embed.FS

// Topics lists the guides in reading order.
var Topics = []string{"workflow", "markdown", "components", "templates", "typst-layouts", "brands", "troubleshooting"}

// Guide returns the Markdown of a topic.
func Guide(topic string) (string, bool) {
	for _, t := range Topics {
		if t == topic {
			b, err := guideFS.ReadFile("guides/" + topic + ".md")
			return string(b), err == nil
		}
	}
	return "", false
}

// GuideIndex lists every topic with its title and summary (the first heading
// and the first paragraph line of each guide).
func GuideIndex() string {
	var sb strings.Builder
	sb.WriteString("# Folio guides\n\nCall get_guide(topic) or read folio://guide/{topic}.\n\n")
	for _, t := range Topics {
		md, _ := Guide(t)
		title, summary := guideHead(md)
		fmt.Fprintf(&sb, "- `%s`: %s. %s\n", t, title, summary)
	}
	return sb.String()
}

// guideHead returns the "# " title and the first non-empty line after it.
func guideHead(md string) (title, summary string) {
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
	return title, summary
}
