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

// TestSkillReferencesLinked checks that SKILL.md links every reference, that
// every link resolves, and that references are one level deep.
func TestSkillReferencesLinked(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	_, body := splitSkill(t, files["SKILL.md"])
	linkRE := regexp.MustCompile(`\]\((references/[^)]+)\)`)
	linked := map[string]bool{}
	for _, m := range linkRE.FindAllStringSubmatch(body, -1) {
		linked[m[1]] = true
		if _, ok := files[m[1]]; !ok {
			t.Errorf("SKILL.md links %s, which is not generated", m[1])
		}
	}
	for name := range files {
		if name == "SKILL.md" {
			continue
		}
		if strings.Count(name, "/") != 1 || !strings.HasPrefix(name, "references/") {
			t.Errorf("%s: references must be files directly under references/", name)
		}
		if !linked[name] {
			t.Errorf("SKILL.md does not link %s", name)
		}
	}
}
