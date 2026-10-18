// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/mcpserver"
)

// readmeExample is one `<!-- folio-example tool=… -->` block of the README.
type readmeExample struct {
	tool, from, total, payments string
	args                        string
}

var exampleRE = regexp.MustCompile(`^<!-- folio-example tool=([a-z_]+)((?: [a-z]+=[^ ]+)*) -->$`)

func readmeExamples(t *testing.T) []readmeExample {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var out []readmeExample
	for i := 0; i < len(lines); i++ {
		m := exampleRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ex := readmeExample{tool: m[1]}
		for _, kv := range strings.Fields(m[2]) {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "from":
				ex.from = v
			case "total":
				ex.total = v
			case "payments":
				ex.payments = v
			default:
				t.Fatalf("README line %d: unknown attribute %q", i+1, k)
			}
		}
		if i+1 >= len(lines) || lines[i+1] != "```json" {
			t.Fatalf("README line %d: a folio-example marker must be followed by a ```json block", i+1)
		}
		var body strings.Builder
		for i += 2; i < len(lines) && lines[i] != "```"; i++ {
			body.WriteString(lines[i] + "\n")
		}
		ex.args = body.String()
		out = append(out, ex)
	}
	return out
}

// TestReadmeExamples runs every marked README example, in order, against a
// seeded instance: the worked examples must keep working as documented.
func TestReadmeExamples(t *testing.T) {
	examples := readmeExamples(t)
	if len(examples) < 8 {
		t.Fatalf("found %d README examples, want at least 8", len(examples))
	}
	h := start(t, nil, nil)
	vars := map[string]string{}
	previous := map[string]map[string]any{} // last arguments per tool
	for i, ex := range examples {
		raw := ex.args
		for k, v := range vars {
			raw = strings.ReplaceAll(raw, "<"+k+">", v)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatalf("example %d (%s): invalid JSON: %v", i+1, ex.tool, err)
		}
		if ex.from != "" {
			base, ok := previous[ex.from]
			if !ok {
				t.Fatalf("example %d (%s): from=%s has no earlier example", i+1, ex.tool, ex.from)
			}
			merged := map[string]any{}
			for k, v := range base {
				merged[k] = v
			}
			for k, v := range args {
				merged[k] = v
			}
			args = merged
		}
		previous[ex.tool] = args
		var res map[string]json.RawMessage
		h.ok(ex.tool, args, &res)
		switch ex.tool {
		case "validate_document":
			var v mcpserver.ValidateOut
			raw, _ := json.Marshal(res)
			_ = json.Unmarshal(raw, &v)
			var c struct {
				Budget     struct{ Total string }
				Milestones struct {
					Items []struct{ Amount string }
				}
			}
			_ = json.Unmarshal(v.Computed, &c)
			var pay []string
			for _, it := range c.Milestones.Items {
				pay = append(pay, it.Amount)
			}
			if !v.Valid || len(v.Findings) != 0 || (ex.total != "" && c.Budget.Total != ex.total) ||
				(ex.payments != "" && strings.Join(pay, ",") != ex.payments) {
				t.Fatalf("example %d: valid=%v findings=%+v total=%s payments=%v (README says %s / %s)",
					i+1, v.Valid, v.Findings, c.Budget.Total, pay, ex.total, ex.payments)
			}
		case "upload_asset":
			var id string
			_ = json.Unmarshal(res["asset_id"], &id)
			vars["asset_id"] = id
		case "render_document":
			var id string
			var warnings []any
			_ = json.Unmarshal(res["document_id"], &id)
			_ = json.Unmarshal(res["warnings"], &warnings)
			if len(warnings) != 0 {
				t.Fatalf("example %d: render warnings %v", i+1, warnings)
			}
			vars["document_id"] = id
		}
	}
}

// TestReadmeConfigurationTable keeps the README configuration table in sync
// with the variables internal/config reads.
func TestReadmeConfigurationTable(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	vars := regexp.MustCompile(`"(FOLIO_[A-Z_]+)"`).FindAllStringSubmatch(string(src), -1)
	var names []string
	for _, m := range vars {
		names = append(names, m[1])
	}
	names = append(names, "FOLIO_PANDOC", "FOLIO_TYPST")
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) < 30 {
		t.Fatalf("found only %d variables in config.go", len(names))
	}
	var rows []string
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "| `FOLIO_") {
			rows = append(rows, line)
		}
	}
	for _, n := range names {
		found := false
		for _, r := range rows {
			if strings.Contains(strings.SplitN(r, "|", 3)[1], "`"+n+"`") {
				found = true
			}
		}
		if !found {
			t.Errorf("README configuration table lacks %s", n)
		}
	}
}
