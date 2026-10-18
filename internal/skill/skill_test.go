// SPDX-License-Identifier: AGPL-3.0-only

package skill

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/vocdoni/folio/internal/mcpserver"
)

var root = filepath.Join("..", "..")

// TestSkillUpToDate fails when skills/folio differs from what the guides
// generate (AC: no drift between get_guide and the skill).
func TestSkillUpToDate(t *testing.T) {
	want, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	seen := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		got, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if w, ok := want[rel]; !ok {
			t.Errorf("%s/%s is not generated; delete it or add a guide", Dir, rel)
		} else if !bytes.Equal(got, w) {
			t.Errorf("%s/%s is stale: run go generate ./internal/skill", Dir, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel := range want {
		if !seen[rel] {
			t.Errorf("%s/%s is missing: run go generate ./internal/skill", Dir, rel)
		}
	}
}

type frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
}

func splitSkill(t *testing.T, b []byte) (frontmatter, string) {
	t.Helper()
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	fm, body, ok := strings.Cut(s[4:], "\n---\n")
	if !ok {
		t.Fatal("SKILL.md frontmatter is not closed")
	}
	var f frontmatter
	dec := yaml.NewDecoder(strings.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("SKILL.md frontmatter: %v", err)
	}
	return f, body
}

// TestSkillFrontmatter checks the Agent Skills format rules.
func TestSkillFrontmatter(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	f, body := splitSkill(t, files["SKILL.md"])
	nameRE := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	if f.Name != filepath.Base(Dir) || len(f.Name) > 64 || !nameRE.MatchString(f.Name) {
		t.Errorf("name %q must equal the directory name and match %s (≤ 64)", f.Name, nameRE)
	}
	if n := len([]rune(f.Description)); n == 0 || n > 1024 {
		t.Errorf("description: %d characters, want 1–1024", n)
	}
	for _, w := range []string{"PDF", "DOCX", "proposal", "quote", "budget", "statement", "report", "brand", "template", "MCP"} {
		if !strings.Contains(f.Description, w) {
			t.Errorf("description should mention %q for triggering", w)
		}
	}
	if n := len([]rune(f.Compatibility)); n > 500 {
		t.Errorf("compatibility: %d characters, want ≤ 500", n)
	}
	if lines := strings.Count(body, "\n"); lines >= 500 {
		t.Errorf("SKILL.md body: %d lines, want < 500", lines)
	}
}

// TestSkillIsOneFileWithGuideIndex checks that the skill is a single
// SKILL.md and that its "Deeper guides" section names every guide topic for
// get_guide.
func TestSkillIsOneFileWithGuideIndex(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files["SKILL.md"] == nil {
		t.Fatalf("the skill must be exactly SKILL.md, got %d files", len(files))
	}
	_, body := splitSkill(t, files["SKILL.md"])
	_, guides, ok := strings.Cut(body, "## Deeper guides")
	if !ok || !strings.Contains(guides, "get_guide") {
		t.Fatal("SKILL.md needs a 'Deeper guides' section that tells the agent to call get_guide")
	}
	for _, topic := range mcpserver.Topics {
		if !strings.Contains(guides, "- `"+topic+"`:") {
			t.Errorf("Deeper guides does not list %q", topic)
		}
	}
	if strings.Contains(body, "references/") || strings.Contains(body, "claude mcp add") {
		t.Error("SKILL.md must not link references/ or show a manual claude mcp add")
	}
}
