// SPDX-License-Identifier: AGPL-3.0-only

package httpserver

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/vocdoni/folio/skills"
)

// TestSkillMarkdownIsPublic: the skill is served without a token (even when
// /mcp is guarded) with the right headers, byte for byte the embedded file.
func TestSkillMarkdownIsPublic(t *testing.T) {
	h, _ := newTestHandler(t, Options{Tokens: tokens})
	w := do(h, http.MethodGet, "/skills/folio/SKILL.md", "", nil)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", got)
	}
	want, err := fs.ReadFile(skills.FS, "folio/SKILL.md")
	if err != nil || w.Body.String() != string(want) || !strings.HasPrefix(w.Body.String(), "---\nname: folio") {
		t.Fatalf("body is not the embedded SKILL.md: %v %.40q", err, w.Body)
	}
}

// TestSkillServesOnlySkillMarkdown: no tarball, no directory listing, no
// other file and no path traversal.
func TestSkillServesOnlySkillMarkdown(t *testing.T) {
	h, _ := newTestHandler(t, Options{Tokens: tokens})
	for _, p := range []string{"/skills", "/skills/", "/skills/folio", "/skills/folio/", "/skills/folio.tar.gz", "/skills/folio/references/workflow.md",
		"/skills/folio/../../go.mod", "/skills/%2e%2e/go.mod", "/skills/folio/SKILL.md/x", "/skills/folio/skill.md"} {
		if w := do(h, http.MethodGet, p, "", nil); w.Code == 200 {
			t.Errorf("%s: status 200, want a refusal", p)
		}
	}
	if w := do(h, http.MethodPost, "/skills/folio/SKILL.md", "", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}
